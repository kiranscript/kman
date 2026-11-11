package gateway

import (
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/kirank55/kman/internal/limiter"
	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/vault"
)

// Routes is where the gateway finds a key: the registry's snapshot. A key the
// checksum rejects never gets this far.
type Routes interface {
	Lookup(keyID string) (registry.Route, bool)
}

// Options is what a Gateway reads.
type Options struct {
	Routes Routes
	Vault  *vault.Vault
	Now    func() time.Time
	Log    *slog.Logger // defaults to discarding; never given a secret or a query string
	// MaxBody is the most of a request body kman holds, defaulting to
	// DefaultMaxBody. Over it, a request is refused.
	MaxBody int64
	// TrustedProxies are the networks whose X-Forwarded-For is believed
	// (kman.yaml's trusted_proxies). With none, the client address is the
	// connection's.
	TrustedProxies []netip.Prefix
	// Limiter holds the RPM windows and the invalid-key counts. It defaults to one
	// with the default bounds, on Now.
	Limiter *limiter.Limiter

	// RootCAs is who an upstream's certificate must chain to, and nil means the
	// system's. Tests set it to trust their own upstream.
	RootCAs *x509.CertPool
	// Transport replaces the real one. It is for tests.
	Transport http.RoundTripper
	// Meter keeps every key's spend. Without one nothing is metered, and a key that
	// must be (one with a spend cap or a model allowlist) is refused rather than let through.
	Meter *meter.Meter
	// OnUsage is told what each response it could read reported, once it has ended.
	// It is a seam for tests that look at what was read, apart from what it cost.
	OnUsage func(registry.Route, profiles.Usage)
}

// Gateway is the proxy listener's http.Handler.
type Gateway struct {
	opts    Options
	limiter *limiter.Limiter
	base    *http.Transport
	named   sync.Map // TLS server name → *http.Transport
}

// New returns a Gateway.
func New(opts Options) *Gateway {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	if opts.Limiter == nil {
		opts.Limiter = limiter.New(limiter.Options{Now: opts.Now})
	}
	return &Gateway{opts: opts, limiter: opts.Limiter, base: newTransport(opts.RootCAs, "")}
}

// exchange is one request on its way through the pipeline.
type exchange struct {
	w     http.ResponseWriter
	r     *http.Request
	route registry.Route
	body  []byte
	// ip is where the request came from, by the connection or a trusted proxy.
	ip netip.Addr
	// usage reads usage from the response as it passes. It is nil before there's a
	// response, and for one that can't be read or isn't metered. What it holds
	// stands after a broken response too, when abort is also set.
	usage *profiles.UsageReader
	// abort means the response can't be finished properly: the connection is cut,
	// so the app can't take what it got for the whole of it.
	abort bool
	// res is the reservation taken against the key's spend, nil when none was.
	// status is the upstream's, or zero while it hasn't answered.
	res    *meter.Reservation
	status int
}

// refusal is why a stage stopped a request, before anything of a response was
// written. An empty message means the code's usual one.
type refusal struct {
	code    code
	message string
	// retryAfter, when set, is how long an app should wait: the Retry-After header.
	retryAfter time.Duration
}

// stage is a step of the pipeline. A refusal ends the request.
type stage func(*Gateway, *exchange) *refusal

// pipeline is the request path up to the upstream's response, in order
// (SPEC §2). The body is read only once a request has a good key, and before
// policy, which reads the model from it.
var pipeline = [...]stage{
	(*Gateway).guarded,
	(*Gateway).buffer,
	(*Gateway).policy,
	(*Gateway).limit,
	(*Gateway).reserve,
	(*Gateway).forward,
}

// ServeHTTP runs one request through the pipeline. However it ends, even in a
// panic, its reservation is settled and its event emitted.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x := exchange{w: w, r: r}
	defer g.finish(&x)
	for _, run := range pipeline {
		if ref := run(g, &x); ref != nil {
			if !x.abort {
				if ref.retryAfter > 0 {
					w.Header()["Retry-After"] = retryAfter(ref.retryAfter)
				}
				writeError(w, ref.code, ref.message)
			}
			break
		}
	}
}

// finish settles the request's spend and emits its event, then cuts the
// connection of a response that broke.
func (g *Gateway) finish(x *exchange) {
	g.settle(x)
	g.emit(x)
	if x.abort {
		panic(http.ErrAbortHandler)
	}
}

// emit will publish the request's event (#46). It runs for every request.
func (g *Gateway) emit(*exchange) {}
