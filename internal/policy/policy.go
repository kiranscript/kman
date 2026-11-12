package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Version is the version of the policy format this kman writes. The store
// keeps it beside each key's policy, so a later format can read an earlier one.
const Version = 1

// Policy is a key's policy: what it is held to. A zero field holds the key to
// nothing, and later tickets add fields, so a stored policy keeps working as
// they do.
type Policy struct {
	// AllowedPaths are path rules (CheckPathRule). When set they are the key's
	// allowlist in place of its upstream's, so a key can be held to less than its
	// upstream allows, or, with no model allowlist, be given a route the upstream's
	// default list leaves out, such as batches.
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	// AllowedModels are exact model ids, or an id ending in * for a prefix.
	AllowedModels []string `json:"allowed_models,omitempty"`
	// AllowedCIDRs and AllowedCountries lock the key to where it may be used from:
	// networks in CIDR form, and ISO 3166-1 country codes (GeoIP). With both, an
	// address must satisfy both. A country lock needs a GeoIP database.
	AllowedCIDRs     []string `json:"allowed_cidrs,omitempty"`
	AllowedCountries []string `json:"allowed_countries,omitempty"`
	// RPMLimit is the most requests a key may make in a minute; 0 is no limit.
	RPMLimit int `json:"rpm_limit,omitempty"`
	// DailyCapMicro and MonthlyCapMicro are the key's spend caps in
	// microdollars, counted in the period of the spend_timezone setting. Zero is
	// no cap.
	DailyCapMicro   int64 `json:"daily_cap_micro,omitzero"`
	MonthlyCapMicro int64 `json:"monthly_cap_micro,omitzero"`
	// DailyBudgetMicro and MonthlyBudgetMicro are soft budgets: amounts that warn
	// and never refuse, with or without a cap (SPEC §7). Where a period has one,
	// its warnings count from the budget in place of the cap. Zero is none.
	DailyBudgetMicro   int64 `json:"daily_budget_micro,omitzero"`
	MonthlyBudgetMicro int64 `json:"monthly_budget_micro,omitzero"`
	// SoftBudgetPercents are the percentages of a cap or budget that warn when
	// crossed, rising: this key's own in place of the install's
	// soft_budget_defaults setting. Empty uses the setting.
	SoftBudgetPercents []int `json:"soft_budget_percents,omitempty"`
}

// RequiresMetering reports whether a key under this policy may send only
// requests kman can meter (SPEC §8, invariant 4). A cap needs the cost of every
// request it holds the key to, and a model allowlist needs the model of each.
func (p Policy) RequiresMetering() bool {
	return p.DailyCapMicro > 0 || p.MonthlyCapMicro > 0 || len(p.AllowedModels) > 0
}

// Limits of what a policy may hold.
const (
	maxRules    = 64
	maxModels   = 256
	maxModelLen = 128
	maxRPM      = 1_000_000
	// maxSoftPercents and maxSoftPercent are the same limits that the
	// soft_budget_defaults setting has.
	maxSoftPercents = 10
	maxSoftPercent  = 1000
)

// check reports the first thing wrong with p.
func (p Policy) check() error {
	if len(p.AllowedPaths) > maxRules {
		return fmt.Errorf("allowed_paths: at most %d rules", maxRules)
	}
	seen := map[string]bool{}
	for _, r := range p.AllowedPaths {
		if err := CheckPathRule(r); err != nil {
			return fmt.Errorf("allowed_paths rule %q: %w", r, err)
		}
		if seen[r] {
			return fmt.Errorf("allowed_paths: %q is listed twice", r)
		}
		seen[r] = true
	}
	if len(p.AllowedModels) > maxModels {
		return fmt.Errorf("allowed_models: at most %d models", maxModels)
	}
	seen = map[string]bool{}
	for _, m := range p.AllowedModels {
		if err := checkModelRule(m); err != nil {
			return fmt.Errorf("allowed_models %q: %w", m, err)
		}
		if seen[m] {
			return fmt.Errorf("allowed_models: %q is listed twice", m)
		}
		seen[m] = true
	}
	if p.RPMLimit < 0 || p.RPMLimit > maxRPM {
		return fmt.Errorf("rpm_limit: use 1 to %d, or 0 for no limit", maxRPM)
	}
	return p.checkSoftBudgets()
}

func (p Policy) checkSoftBudgets() error {
	if p.SoftBudgetPercents != nil {
		if len(p.SoftBudgetPercents) == 0 || len(p.SoftBudgetPercents) > maxSoftPercents {
			return fmt.Errorf("soft_budget_percents: list 1 to %d percentages, or leave it out to use the install's", maxSoftPercents)
		}
		prev := 0
		for _, pc := range p.SoftBudgetPercents {
			if pc <= prev || pc > maxSoftPercent {
				return fmt.Errorf("soft_budget_percents: use rising percentages from 1 to %d", maxSoftPercent)
			}
			prev = pc
		}
	}
	for _, b := range []struct {
		name          string
		budget, limit int64
	}{
		{"daily", p.DailyBudgetMicro, p.DailyCapMicro},
		{"monthly", p.MonthlyBudgetMicro, p.MonthlyCapMicro},
	} {
		if b.budget > 0 && b.limit > 0 && b.budget > b.limit {
			return fmt.Errorf("%s_budget_micro is above %s_cap_micro: a soft budget warns before the cap refuses, not after", b.name, b.name)
		}
	}
	if p.DailyBudgetMicro > 0 && p.MonthlyBudgetMicro > 0 && p.MonthlyBudgetMicro < p.DailyBudgetMicro {
		return errors.New("monthly_budget_micro is below daily_budget_micro, and a month holds a day")
	}
	return nil
}

func checkModelRule(m string) error {
	switch {
	case m == "":
		return errors.New("a model id is not empty")
	case m == "*":
		return errors.New("to allow every model, leave the allowlist out")
	case len(m) > maxModelLen:
		return fmt.Errorf("at most %d bytes", maxModelLen)
	case strings.Count(m, "*") > 1 || (strings.Contains(m, "*") && !strings.HasSuffix(m, "*")):
		return errors.New("* may only end an id, as a prefix")
	case strings.ContainsFunc(m, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return errors.New("no spaces or control characters")
	}
	return nil
}

// wire is a policy as written: a cap is a pointer, so that a cap of zero can be
// told from none and refused.
type wire struct {
	AllowedPaths     []string `json:"allowed_paths"`
	AllowedModels    []string `json:"allowed_models"`
	AllowedCIDRs     []string `json:"allowed_cidrs"`
	AllowedCountries []string `json:"allowed_countries"`
	RPMLimit         int      `json:"rpm_limit"`
	DailyCapMicro    *int64   `json:"daily_cap_micro"`
	MonthlyCapMicro  *int64   `json:"monthly_cap_micro"`

	DailyBudgetMicro   *int64 `json:"daily_budget_micro"`
	MonthlyBudgetMicro *int64 `json:"monthly_budget_micro"`
	SoftBudgetPercents []int  `json:"soft_budget_percents"`
}

// decode reads a policy, refusing a field kman does not know, since a mistyped
// limit that silently limits nothing is worse than an error.
func decode(raw []byte) (Policy, error) {
	if first := bytes.TrimSpace(raw); len(first) == 0 || first[0] != '{' {
		return Policy{}, errors.New("a policy is a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return Policy{}, fmt.Errorf("the policy: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Policy{}, errors.New("the policy holds more than one JSON value")
	}
	p := Policy{AllowedPaths: w.AllowedPaths, AllowedModels: w.AllowedModels, RPMLimit: w.RPMLimit, SoftBudgetPercents: w.SoftBudgetPercents}
	var err error
	if p.AllowedCIDRs, p.AllowedCountries, err = normalizeLocks(w.AllowedCIDRs, w.AllowedCountries); err != nil {
		return Policy{}, fmt.Errorf("the policy: %w", err)
	}
	for _, c := range []struct {
		name string
		in   *int64
		out  *int64
	}{
		{"daily_cap_micro", w.DailyCapMicro, &p.DailyCapMicro},
		{"monthly_cap_micro", w.MonthlyCapMicro, &p.MonthlyCapMicro},
		{"daily_budget_micro", w.DailyBudgetMicro, &p.DailyBudgetMicro},
		{"monthly_budget_micro", w.MonthlyBudgetMicro, &p.MonthlyBudgetMicro},
	} {
		if c.in == nil {
			continue
		}
		if *c.in <= 0 {
			return Policy{}, fmt.Errorf("the policy: %s must be above 0 microdollars; leave it out for no cap", c.name)
		}
		*c.out = *c.in
	}
	if p.DailyCapMicro > 0 && p.MonthlyCapMicro > 0 && p.MonthlyCapMicro < p.DailyCapMicro {
		return Policy{}, errors.New("the policy: monthly_cap_micro is below daily_cap_micro, and a month holds a day")
	}
	return p, nil
}

// Parse reads a stored policy and checks it. An empty one is the empty policy.
func Parse(raw string) (Policy, error) {
	if raw == "" {
		return Policy{}, nil
	}
	p, err := decode([]byte(raw))
	if err != nil {
		return Policy{}, err
	}
	if err := p.check(); err != nil {
		return Policy{}, fmt.Errorf("the policy: %w", err)
	}
	return p, nil
}

// Normalize checks a policy as JSON and returns its version and the form to
// store. Only a JSON object is a policy.
func Normalize(raw []byte) (version int, normalized []byte, err error) {
	p, err := decode(raw)
	if err != nil {
		return 0, nil, err
	}
	if err := p.check(); err != nil {
		return 0, nil, fmt.Errorf("the policy: %w", err)
	}
	normalized, err = json.Marshal(p)
	if err != nil {
		return 0, nil, fmt.Errorf("the policy: %w", err)
	}
	return Version, normalized, nil
}
