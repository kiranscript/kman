package policy

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// Compiled is a key's policy ready for the request path. It is built once, when
// the registry's snapshot is, and is immutable: Evaluate reads it and parses
// nothing.
type Compiled struct {
	paths     *Paths // the key's own list, or nil for its upstream's
	exact     map[string]struct{}
	prefixes  []string
	cidrs     []netip.Prefix // the address lock's networks, none for no lock
	countries []string       // and its countries, in capitals
	models    bool           // an allowlist is set, which may be empty only by being absent
	rpm       int
	daily     int64
	monthly   int64
	strict    bool  // the key may send only requests kman can meter
	broken    error // why the policy can't be read, if it can't
}

// Compile builds the compiled form of a stored policy: the JSON and the version
// policy.Normalize gave. A policy that can't be read, such as one from a newer
// kman, still returns a usable value, which allows no request: failing open would
// drop a key's limits. The error says why, for a log.
func Compile(version int, stored string) (*Compiled, error) {
	fail := func(err error) (*Compiled, error) { return &Compiled{broken: err}, err }
	if version != Version {
		return fail(fmt.Errorf("policy version %d: this kman reads version %d", version, Version))
	}
	_, normalized, err := Normalize([]byte(stored))
	if err != nil {
		return fail(err)
	}
	var p Policy
	if err := json.Unmarshal(normalized, &p); err != nil {
		return fail(err)
	}
	c := &Compiled{rpm: p.RPMLimit, daily: p.DailyCapMicro, monthly: p.MonthlyCapMicro, strict: p.RequiresMetering()}
	c.countries = p.AllowedCountries
	for _, n := range p.AllowedCIDRs {
		prefix, err := netip.ParsePrefix(n)
		if err != nil {
			return fail(err)
		}
		c.cidrs = append(c.cidrs, prefix)
	}
	if len(p.AllowedPaths) > 0 {
		if c.paths, err = CompilePaths(p.AllowedPaths); err != nil {
			return fail(err)
		}
	}
	if len(p.AllowedModels) > 0 {
		c.models = true
		c.exact = make(map[string]struct{}, len(p.AllowedModels))
		for _, m := range p.AllowedModels {
			if prefix, ok := strings.CutSuffix(m, "*"); ok {
				c.prefixes = append(c.prefixes, prefix)
			} else {
				c.exact[m] = struct{}{}
			}
		}
	}
	return c, nil
}

// Err says why a policy can't be read, and is nil for one that can. Such a
// policy allows no request, so a caller that refuses one may name the reason.
func (c *Compiled) Err() error {
	if c == nil {
		return nil
	}
	return c.broken
}

// RPM is the key's requests-per-minute limit, 0 for none.
func (c *Compiled) RPM() int {
	if c == nil {
		return 0
	}
	return c.rpm
}

// Caps are the key's daily and monthly spend caps in microdollars, 0 for none.
func (c *Compiled) Caps() (daily, monthly int64) {
	if c == nil {
		return 0, 0
	}
	return c.daily, c.monthly
}

// RequiresMetering reports whether the key may send only requests kman can meter
// (Policy.RequiresMetering). A policy that can't be read requires it.
func (c *Compiled) RequiresMetering() bool { return c == nil || c.broken != nil || c.strict }

// NeedsModel reports whether Evaluate reads Facts.Model, so a caller that has to
// read it out of a body may skip that when it doesn't.
func (c *Compiled) NeedsModel() bool { return c == nil || c.models }

// Facts are what Evaluate decides from.
type Facts struct {
	Method string // as sent, "POST"
	Path   string // the escaped path as the app sent it
	// Model is the request's model, and HasModel says kman could read one (see
	// ModelOf). It is read only when the key has a model allowlist.
	Model    string
	HasModel bool
	// Upstream is the upstream's path allowlist.
	Upstream *Paths
}

// Decision is what policy says to do with a request.
type Decision uint8

// The decisions. All but Allow are refusals, whose names are SPEC §6's codes.
const (
	Allow Decision = iota
	PathNotAllowed
	ModelNotAllowed
	Unmetered
	AddressNotAllowed
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case PathNotAllowed:
		return "path_not_allowed"
	case ModelNotAllowed:
		return "model_not_allowed"
	case Unmetered:
		return "unmetered_request"
	case AddressNotAllowed:
		return "address_not_allowed"
	}
	return fmt.Sprintf("decision(%d)", uint8(d))
}

// Evaluate decides whether a request may go on. It is a pure function of the
// facts and the policy, never reading the clock or anything else.
//
// The path is held to the upstream's allowlist, or to the key's own when it has
// one; a key with a model allowlist or a spend cap and its own list must satisfy
// both, so that neither can be sidestepped by a route kman reads no model from. A
// request with no model a key's allowlist can be checked against is an unmetered
// request, since it could name any model upstream.
func Evaluate(f Facts, c *Compiled) Decision {
	if c == nil || c.broken != nil {
		return PathNotAllowed
	}
	switch {
	case c.paths == nil:
		if !f.Upstream.Match(f.Method, f.Path) {
			return PathNotAllowed
		}
	case !c.paths.Match(f.Method, f.Path), (c.models || c.daily > 0 || c.monthly > 0) && !f.Upstream.Match(f.Method, f.Path):
		return PathNotAllowed
	}
	if c.models {
		if !f.HasModel {
			return Unmetered
		}
		if !c.allowsModel(f.Model) {
			return ModelNotAllowed
		}
	}
	return Allow
}

func (c *Compiled) allowsModel(m string) bool {
	if _, ok := c.exact[m]; ok && m != "" {
		return true
	}
	for _, p := range c.prefixes {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}
