package admin

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// The rules Exposure checks. A key can break more than one, and each is its own
// finding.
const (
	ExposureOpenToEveryAddress = "open_to_every_address"
	ExposureNeverExpires       = "never_expires"
	ExposureUnused             = "unused"
)

// unusedAfter is how long a key may sit idle before Exposure reports it. A key
// that is not used is a key nobody would miss, and nobody would notice being
// used.
const unusedAfter = 30 * 24 * time.Hour

// Exposure is the keys that would be easy to abuse, each with the command that
// fixes it. It is empty when there is nothing to fix.
type Exposure struct {
	Findings    []ExposureFinding `json:"findings"`
	KeysChecked int               `json:"keys_checked" doc:"How many keys were looked at: the keys that work now. A disabled, revoked or expired key cannot be abused, and a canary is meant to be left alone."`
}

// ExposureFinding is one rule one key breaks.
type ExposureFinding struct {
	KeyID      string     `json:"key_id"`
	Name       string     `json:"name,omitempty"`
	Rule       string     `json:"rule" doc:"open_to_every_address, never_expires or unused."`
	Message    string     `json:"message"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" doc:"For unused: the start of the last hour in which the key forwarded a request. Absent if it never has."`
	Fix        string     `json:"fix" doc:"The kman command that fixes it, ready to run. An open key's has a placeholder to replace with the network the key is used from."`
}

// Exposure finds the keys that work now and would be easy to abuse if they
// leaked: open to every address (no allowed_cidrs and no allowed_countries,
// SPEC §6), never expiring, or not used for 30 days. It reads and changes
// nothing, and works from usage_hourly, so "used" is as fine as an hour. A
// request kman refused is not a use.
func (s *Service) Exposure(ctx context.Context) (Exposure, error) {
	out := Exposure{Findings: []ExposureFinding{}}
	now := s.now()
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := tx.Keys()
		if err != nil {
			return err
		}
		used, err := tx.LastUsed()
		if err != nil {
			return err
		}
		settings, err := loadSettings(tx)
		if err != nil {
			return err
		}
		for _, k := range rows {
			if k.Status != store.KeyActive || k.Kind == store.KeyCanary || (!k.ExpiresAt.IsZero() && !now.Before(k.ExpiresAt)) {
				continue
			}
			out.KeysChecked++
			ref := cmp.Or(k.Name, k.ID)
			found := func(rule, message, fix string) ExposureFinding {
				return ExposureFinding{KeyID: k.ID, Name: k.Name, Rule: rule, Message: message, Fix: fix}
			}
			open, err := openToEveryAddress(k.PolicyJSON)
			if err != nil {
				return fmt.Errorf("admin: key %s: policy: %w", k.ID, err)
			}
			if open {
				fix, err := lockFix(ref, k.PolicyJSON)
				if err != nil {
					return fmt.Errorf("admin: key %s: policy: %w", k.ID, err)
				}
				out.Findings = append(out.Findings, found(ExposureOpenToEveryAddress,
					"It is open to every address: its policy has no allowed_cidrs and no allowed_countries, so anyone who holds it can use it from anywhere. Replace <network/bits> with the network it is used from.", fix))
			}
			if k.ExpiresAt.IsZero() {
				message, fix := neverExpiresFix(ref, k, settings, now)
				out.Findings = append(out.Findings, found(ExposureNeverExpires, message, fix))
			}
			idleSince, last := k.CreatedAt, used[k.ID]
			if !last.IsZero() {
				idleSince = last
			}
			if idle := now.Sub(idleSince); idle >= unusedAfter {
				f := found(ExposureUnused, "", "kman key disable "+ref)
				days := int(idle / (24 * time.Hour))
				if last.IsZero() {
					f.Message = fmt.Sprintf("It has never been used, and it was created %d days ago.", days)
				} else {
					f.LastUsedAt = &last
					f.Message = fmt.Sprintf("It was last used %s, %d days ago.", last.UTC().Format(time.DateOnly), days)
				}
				f.Message += " Disable it, and enable it again if something needs it."
				out.Findings = append(out.Findings, f)
			}
		}
		return nil
	})
	if err != nil {
		return Exposure{}, err
	}
	return out, nil
}

// openToEveryAddress reports whether a stored policy has neither an address
// lock nor a country lock.
func openToEveryAddress(policy string) (bool, error) {
	var p struct {
		CIDRs     []string `json:"allowed_cidrs"`
		Countries []string `json:"allowed_countries"`
	}
	if err := json.Unmarshal([]byte(policy), &p); err != nil {
		return false, err
	}
	return len(p.CIDRs) == 0 && len(p.Countries) == 0, nil
}

// lockFix is the command that locks a key to a network: its policy with
// allowed_cidrs added, since --policy replaces the whole policy.
func lockFix(ref, policy string) (string, error) {
	p := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(policy), &p); err != nil {
		return "", err
	}
	p["allowed_cidrs"] = json.RawMessage(`["<network/bits>"]`)
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return "kman key update " + ref + " --policy " + shellQuote(string(raw)), nil
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// neverExpiresFix is what to tell the operator of a key with no expiry. The
// key can be given the default TTL, or what is left of the maximum key
// lifetime if that is less, and when none is left it can only be replaced.
func neverExpiresFix(ref string, k store.VirtualKey, s Settings, now time.Time) (message, fix string) {
	ttl := min(seconds(s.DefaultKeyTTLSeconds), k.CreatedAt.Add(seconds(s.MaxKeyLifetimeSeconds)).Sub(now))
	if ttl < time.Hour {
		return "It never expires, and it is older than the maximum key lifetime, so it cannot be given an expiry. Revoke it and create a replacement with kman key create.",
			"kman key revoke " + ref
	}
	hours := int64(ttl / time.Hour)
	text := fmt.Sprintf("%dh", hours)
	if hours%24 == 0 {
		text = fmt.Sprintf("%dd", hours/24)
	}
	return "It never expires. Give it an expiry.", "kman key update " + ref + " --ttl " + text
}
