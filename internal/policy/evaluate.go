package policy

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Compiled is a key's policy ready for the request path. It is built once, when
// the registry's snapshot is, and is immutable: Evaluate reads it and parses
// nothing.
type Compiled struct {
	paths    *Paths // the key's own list, or nil for its upstream's
	exact    map[string]struct{}
	prefixes []string
	models   bool // an allowlist is set, which may be empty only by being absent
	rpm      int
	broken   error // why the policy can't be read, if it can't
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
	c := &Compiled{rpm: p.RPMLimit}
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
	}
	return fmt.Sprintf("decision(%d)", uint8(d))
}

// Evaluate decides whether a request may go on. It is a pure function of the
// facts and the policy, never reading the clock or anything else.
//
// The path is held to the upstream's allowlist, or to the key's own when it has
// one; a key with a model allowlist and its own list must satisfy both, so that
// a model allowlist can't be sidestepped by a route kman reads no model from. A
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
	case !c.paths.Match(f.Method, f.Path), c.models && !f.Upstream.Match(f.Method, f.Path):
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
