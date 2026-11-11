package gateway

import (
	"strconv"
	"time"

	"github.com/kirank55/kman/internal/policy"
)

// policy holds a request to its key's policy: the upstream's path allowlist or
// the key's own, and the key's model allowlist. Both read what the registry
// compiled when it was published, and never parse anything but the model out of
// the body.
func (g *Gateway) policy(x *exchange) *refusal {
	c := x.route.Key.Compiled
	facts := policy.Facts{
		Method: x.r.Method,
		// The escaped path, as sent, so what is matched is what is forwarded.
		Path:     x.r.URL.EscapedPath(),
		Upstream: x.route.Upstream.Rules,
	}
	if c.NeedsModel() {
		facts.Model, facts.HasModel = policy.ModelOf(x.body)
	}
	switch policy.Evaluate(facts, c) {
	case policy.Allow:
		return nil
	case policy.ModelNotAllowed:
		return &refusal{code: codeModelNotAllowed}
	case policy.Unmetered:
		return &refusal{code: codeUnmeteredRequest, message: "This virtual key is limited to certain models, and kman cannot read a model from this request."}
	}
	if err := c.Err(); err != nil {
		g.opts.Log.Error("a key's policy cannot be read, so it allows nothing", "key", x.route.Key.ID, "err", err)
	}
	return &refusal{code: codePathNotAllowed}
}

// limit holds a key to its requests-per-minute limit.
func (g *Gateway) limit(x *exchange) *refusal {
	ok, retry := g.limiter.Allow(x.route.Key.ID, x.route.Key.Compiled.RPM())
	if ok {
		return nil
	}
	return &refusal{code: codeRateLimited, retryAfter: retry}
}

// guarded runs authenticate behind the throttle on invalid keys. An address that
// has presented too many in the last minute is refused before its key is read,
// so it can't guess at the speed of the server, and a good key from it waits
// too. A key that is revoked, expired or disabled was presented by someone who
// holds its secret, and isn't counted.
func (g *Gateway) guarded(x *exchange) *refusal {
	x.ip = clientIP(x.r, g.opts.TrustedProxies)
	if !g.limiter.AllowInvalid(x.ip) {
		return &refusal{code: codeRateLimited, retryAfter: time.Minute,
			message: "Too many invalid virtual keys have come from this address. Try again in a minute."}
	}
	ref := g.authenticate(x)
	if ref != nil && ref.code == codeInvalidKey {
		g.limiter.RecordInvalid(x.ip)
	}
	return ref
}

// retryAfterSeconds is a Retry-After value: whole seconds, rounded up.
func retryAfterSeconds(d time.Duration) string {
	return strconv.FormatInt(int64((d+time.Second-1)/time.Second), 10)
}
