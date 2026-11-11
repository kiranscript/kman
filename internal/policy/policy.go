package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Version is the version of the policy format this kman writes. The store
// keeps it beside each key's policy, so a later format can read an earlier one.
const Version = 1

// Policy is a key's policy: what it is held to. A zero field holds the key to
// nothing, and later tickets add fields, so a stored policy keeps working as
// they do.
type Policy struct {
	// DailyCapMicro and MonthlyCapMicro are the key's spend caps in
	// microdollars, counted in the period of the spend_timezone setting. Zero is
	// no cap.
	DailyCapMicro   int64 `json:"daily_cap_micro,omitzero"`
	MonthlyCapMicro int64 `json:"monthly_cap_micro,omitzero"`
}

// RequiresMetering reports whether a key under this policy may send only
// requests kman can meter (SPEC §8, invariant 4). A cap needs the cost of every
// request it holds the key to.
func (p Policy) RequiresMetering() bool {
	return p.DailyCapMicro > 0 || p.MonthlyCapMicro > 0
}

// wire is a policy as written: a cap is a pointer, so that a cap of zero can be
// told from none and refused.
type wire struct {
	DailyCapMicro   *int64 `json:"daily_cap_micro"`
	MonthlyCapMicro *int64 `json:"monthly_cap_micro"`
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
	var p Policy
	for _, c := range []struct {
		name string
		in   *int64
		out  *int64
	}{
		{"daily_cap_micro", w.DailyCapMicro, &p.DailyCapMicro},
		{"monthly_cap_micro", w.MonthlyCapMicro, &p.MonthlyCapMicro},
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

// Parse reads a stored policy. An empty one is the empty policy.
func Parse(raw string) (Policy, error) {
	if raw == "" {
		return Policy{}, nil
	}
	return decode([]byte(raw))
}

// Normalize checks a policy as JSON and returns its version and the form to
// store. Only a JSON object is a policy.
func Normalize(raw []byte) (version int, normalized []byte, err error) {
	p, err := decode(raw)
	if err != nil {
		return 0, nil, err
	}
	normalized, err = json.Marshal(p)
	if err != nil {
		return 0, nil, fmt.Errorf("the policy: %w", err)
	}
	return Version, normalized, nil
}
