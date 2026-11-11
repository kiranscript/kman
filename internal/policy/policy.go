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

// Policy is a key's policy. Later tickets add fields, and a key's stored policy
// keeps working as they do.
type Policy struct {
	// AllowedPaths are path rules (CheckPathRule). When set they are the key's
	// allowlist in place of its upstream's, so a key can be held to less than its
	// upstream allows, or, with no model allowlist, be given a route the upstream's
	// default list leaves out, such as batches.
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	// AllowedModels are exact model ids, or an id ending in * for a prefix.
	AllowedModels []string `json:"allowed_models,omitempty"`
	// RPMLimit is the most requests a key may make in a minute; 0 is no limit.
	RPMLimit int `json:"rpm_limit,omitempty"`
}

// Limits of what a policy may hold.
const (
	maxRules    = 64
	maxModels   = 256
	maxModelLen = 128
	maxRPM      = 1_000_000
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

// Normalize checks a policy as JSON and returns its version and the form to
// store. Only a JSON object is a policy, and a field kman does not know is
// refused rather than kept, since a mistyped limit that silently limits nothing
// is worse than an error.
func Normalize(raw []byte) (version int, normalized []byte, err error) {
	if first := bytes.TrimSpace(raw); len(first) == 0 || first[0] != '{' {
		return 0, nil, errors.New("a policy is a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return 0, nil, fmt.Errorf("the policy: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return 0, nil, errors.New("the policy holds more than one JSON value")
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
