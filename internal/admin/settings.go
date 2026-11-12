package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// Settings are what an operator changes while kman runs (SPEC §3), as
// opposed to kman.yaml, which is read at startup.
type Settings struct {
	MaxKeyLifetimeSeconds int64  `json:"max_key_lifetime_seconds" doc:"The longest any virtual key may stay valid. No key is created or extended past it. Default 365 days."`
	DefaultKeyTTLSeconds  int64  `json:"default_key_ttl_seconds" doc:"How long a new virtual key stays valid unless told otherwise. At most the maximum key lifetime. Default 90 days."`
	SpendTimezone         string `json:"spend_timezone" doc:"The IANA time zone in which daily and monthly spend periods start at 00:00. Default UTC. A change takes effect when kman restarts."`
	SoftBudgetPercents    []int  `json:"soft_budget_defaults" doc:"The percentages of each spend cap or soft budget that raise a warning when crossed, rising, for keys whose policy sets none (soft_budget_percents). Each warns once per day or month. Default 50, 80, 100; an empty list warns for no key that sets none. A change takes effect at the next check, within a minute."`
}

// SettingsUpdate changes the settings it sets and leaves the rest. The
// result is checked as a whole, so a lifetime and a TTL can move together.
type SettingsUpdate struct {
	MaxKeyLifetimeSeconds *int64  `json:"max_key_lifetime_seconds,omitempty"`
	DefaultKeyTTLSeconds  *int64  `json:"default_key_ttl_seconds,omitempty"`
	SpendTimezone         *string `json:"spend_timezone,omitempty"`
	SoftBudgetPercents    *[]int  `json:"soft_budget_defaults,omitempty"`
}

// The settings' keys, in the settings table and the audit log.
const (
	settingMaxKeyLifetime = "max_key_lifetime"
	settingDefaultKeyTTL  = "default_key_ttl"
	settingSpendTimezone  = "spend_timezone"
	settingSoftBudgets    = "soft_budget_defaults"
)

const day = 24 * 60 * 60

// DefaultSettings are an install's settings until an operator changes them.
func DefaultSettings() Settings {
	return Settings{
		MaxKeyLifetimeSeconds: 365 * day,
		DefaultKeyTTLSeconds:  90 * day,
		SpendTimezone:         "UTC",
		SoftBudgetPercents:    []int{50, 80, 100},
	}
}

// Limits on the settings. A key lifetime is finite: "never" is refused.
const (
	minLifetime    = 60 * 60    // an hour
	maxLifetime    = 3650 * day // ten years
	maxSoftBudgets = 10
	maxSoftPercent = 1000
)

// setting is one setting's key and value.
type setting struct {
	key   string
	value any
}

// list is s as its settings, in a fixed order.
func (s Settings) list() []setting {
	return []setting{
		{settingMaxKeyLifetime, s.MaxKeyLifetimeSeconds},
		{settingDefaultKeyTTL, s.DefaultKeyTTLSeconds},
		{settingSpendTimezone, s.SpendTimezone},
		{settingSoftBudgets, s.SoftBudgetPercents},
	}
}

// load sets the setting key from its stored JSON. A key kman does not know
// is skipped, so a newer kman's settings do not break this one.
func (s *Settings) load(key string, raw []byte) error {
	var dst any
	switch key {
	case settingMaxKeyLifetime:
		dst = &s.MaxKeyLifetimeSeconds
	case settingDefaultKeyTTL:
		dst = &s.DefaultKeyTTLSeconds
	case settingSpendTimezone:
		dst = &s.SpendTimezone
	case settingSoftBudgets:
		dst = &s.SoftBudgetPercents
	default:
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// apply returns s with u applied, and whether u names any setting.
func (s Settings) apply(u SettingsUpdate) (Settings, bool) {
	named := false
	if u.MaxKeyLifetimeSeconds != nil {
		s.MaxKeyLifetimeSeconds, named = *u.MaxKeyLifetimeSeconds, true
	}
	if u.DefaultKeyTTLSeconds != nil {
		s.DefaultKeyTTLSeconds, named = *u.DefaultKeyTTLSeconds, true
	}
	if u.SpendTimezone != nil {
		s.SpendTimezone, named = *u.SpendTimezone, true
	}
	if u.SoftBudgetPercents != nil {
		s.SoftBudgetPercents, named = slices.Clone(*u.SoftBudgetPercents), true
	}
	return s, named
}

// Settings returns the install's settings, defaults filled in.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	err := s.read(ctx, func(tx *store.Tx) error {
		var err error
		out, err = loadSettings(tx)
		return err
	})
	return out, err
}

// UpdateSettings applies u, auditing each setting it changes. Setting a
// value to what it already is records nothing.
func (s *Service) UpdateSettings(ctx context.Context, u SettingsUpdate) (Settings, error) {
	var out Settings
	err := s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		cur, err := loadSettings(tx)
		if err != nil {
			return nil, err
		}
		next, named := cur.apply(u)
		if !named {
			return nil, errorf(CodeInvalid, "the update names no setting")
		}
		if err := next.check(); err != nil {
			return nil, err
		}
		var entries []audit
		before := cur.list()
		for i, after := range next.list() {
			raw, err := json.Marshal(after.value)
			if err != nil {
				return nil, err
			}
			if old, _ := json.Marshal(before[i].value); string(old) == string(raw) {
				continue
			}
			if _, err := tx.PutSetting(store.Setting{Key: after.key, ValueJSON: string(raw), UpdatedAt: now}); err != nil {
				return nil, err
			}
			entries = append(entries, audit{action: "settings.set", kind: "setting", id: after.key,
				before: before[i].value, after: after.value})
		}
		out = next
		return entries, nil
	})
	return out, err
}

// loadSettings reads the settings table over the defaults.
func loadSettings(tx *store.Tx) (Settings, error) {
	out := DefaultSettings()
	rows, err := tx.Settings()
	if err != nil {
		return Settings{}, err
	}
	for _, r := range rows {
		if err := out.load(r.Key, []byte(r.ValueJSON)); err != nil {
			return Settings{}, fmt.Errorf("admin: setting %s holds %s: %w", r.Key, r.ValueJSON, err)
		}
	}
	return out, nil
}

// check enforces the rules on settings as a whole.
func (s Settings) check() error {
	for _, d := range []struct {
		name string
		v    int64
	}{{settingMaxKeyLifetime, s.MaxKeyLifetimeSeconds}, {settingDefaultKeyTTL, s.DefaultKeyTTLSeconds}} {
		if d.v < minLifetime || d.v > maxLifetime {
			return errorf(CodeInvalid, "%s must be between 1 hour and 3650 days", d.name)
		}
	}
	if s.DefaultKeyTTLSeconds > s.MaxKeyLifetimeSeconds {
		return errorf(CodeInvalid, "default_key_ttl must not exceed max_key_lifetime")
	}
	if s.SpendTimezone == "" || s.SpendTimezone == "Local" {
		return errorf(CodeInvalid, "spend_timezone must name an IANA time zone, such as UTC or Europe/Berlin")
	}
	if _, err := time.LoadLocation(s.SpendTimezone); err != nil {
		return errorf(CodeInvalid, "spend_timezone %q is not a time zone kman knows", s.SpendTimezone)
	}
	if len(s.SoftBudgetPercents) > maxSoftBudgets {
		return errorf(CodeInvalid, "soft_budget_defaults holds at most %d percentages", maxSoftBudgets)
	}
	prev := 0
	for _, p := range s.SoftBudgetPercents {
		if p <= prev || p > maxSoftPercent {
			return errorf(CodeInvalid, "soft_budget_defaults must be rising percentages from 1 to %d", maxSoftPercent)
		}
		prev = p
	}
	return nil
}
