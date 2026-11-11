package gateway

import (
	"cmp"
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/limiter"
	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/policy/geoip"
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
	// BodyStall is how long a request body may go without a byte arriving, and
	// BodyTimeout how long it may take whole, whatever its pace. Either ends the
	// connection. They default to DefaultBodyStall and DefaultBodyTimeout.
	BodyStall, BodyTimeout time.Duration
	// MaxBuffered is the most request-body bytes kman holds in memory at once,
	// across every request. Past it a request is refused with a 429 rather than
	// buffered. It defaults to DefaultMaxBuffered.
	MaxBuffered int64
	// TrustedProxies are the networks whose X-Forwarded-For is believed
	// (kman.yaml's trusted_proxies). With none, the client address is the
	// connection's.
	TrustedProxies []netip.Prefix
	// Geo places a client address by country and ASN. It is nil with no GeoIP
	// database (geoip in kman.yaml), and then a key locked to countries is refused.
	Geo Geo
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
	// Events receives one event for every request, however it ends (SPEC §9). It is
	// never waited on: with a full queue an event is dropped and counted. Without
	// one, no events are made.
	Events *events.Bus
	// OnUsage is told what each response it could read reported, once it has ended.
	// It is a seam for tests that look at what was read, apart from what it cost.
	OnUsage func(registry.Route, profiles.Usage)

	// observe is told each request's address and place as it is emitted. It is for tests.
	observe func(netip.Addr, geoip.Info)
}

// Gateway is the proxy listener's http.Handler.
type Gateway struct {
	opts    Options
	limiter *limiter.Limiter
	base    *http.Transport
	named   sync.Map // TLS server name → *http.Transport
	// buffered is the bytes of request bodies held now, by requests in flight.
	buffered atomic.Int64
}

// New returns a Gateway.
func New(opts Options) *Gateway {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	if opts.BodyStall <= 0 {
		opts.BodyStall = DefaultBodyStall
	}
	if opts.BodyTimeout <= 0 {
		opts.BodyTimeout = DefaultBodyTimeout
	}
	if opts.MaxBuffered <= 0 {
		opts.MaxBuffered = DefaultMaxBuffered
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
	// geo is where the address is, when a GeoIP database says. It is what the
	// request's event carries as its country and ASN.
	geo geoip.Info
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
	// settled is whether settle has run: it may run before the response's last byte.
	settled bool
	// held is how many bytes of its body the request has counted against the
	// gateway's MaxBuffered, until it ends.
	held int64

	// What the event is made of.
	start time.Time
	// keyID is the key the request proved it holds, even when the key was then
	// refused: a request with a bad secret is attributed to no key.
	keyID string
	// priority sends the event down the priority lane: a disabled key or a canary.
	priority bool
	// refused is the code the app was refused with, when it was.
	refused   code
	model     string // the model asked for, as the meter read it
	respBytes int64
	reported  profiles.Usage // what the response reported, once settled
	charge    meter.Charge
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
	(*Gateway).address,
	(*Gateway).buffer,
	(*Gateway).policy,
	(*Gateway).limit,
	(*Gateway).reserve,
	(*Gateway).forward,
}

// ServeHTTP runs one request through the pipeline. However it ends, even in a
// panic, its reservation is settled and its event emitted.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x := exchange{w: w, r: r, start: g.opts.Now()}
	defer g.finish(&x)
	for _, run := range pipeline {
		if ref := run(g, &x); ref != nil {
			x.refused = ref.code
			if !x.abort {
				if x.body == nil && r.Body != nil && r.Body != http.NoBody {
					// What is left of the body is unread. net/http would read it, with no
					// limit on the wait, before it let the refusal out: a client with no key
					// could hold the connection by promising a body. Close instead.
					w.Header().Set("Connection", "close")
				}
				if ref.retryAfter > 0 {
					w.Header()["Retry-After"] = retryAfter(ref.retryAfter)
				}
				g.release(&x) // the refusal may follow a reservation (the upstream was down)
				writeError(w, ref.code, ref.message)
			}
			break
		}
	}
}

// release gives back what the request holds: its share of the body budget, and
// its reservation, which it settles. It runs once, before the app has the whole
// response where that can be told (respond, a refusal), and else in finish.
func (g *Gateway) release(x *exchange) {
	g.buffered.Add(-x.held)
	x.held = 0
	g.settle(x)
}

// finish releases whatever the request still holds and emits its event, then cuts
// the connection of a response that broke.
func (g *Gateway) finish(x *exchange) {
	g.release(x)
	g.emit(x)
	if x.abort {
		panic(http.ErrAbortHandler)
	}
}

// maxOperationPath bounds the path an event keeps.
const maxOperationPath = 256

// emit publishes the request's event (SPEC §9). It runs for every request, and
// never waits: Emit drops the event when the queue is full. The event holds what
// happened, never what was said.
func (g *Gateway) emit(x *exchange) {
	if g.opts.observe != nil {
		g.opts.observe(x.ip, x.geo)
	}
	if g.opts.Events == nil {
		return
	}
	e := events.Event{
		Time: x.start, Kind: events.KindRequest, KeyID: x.keyID, ClientIP: x.ip,
		Method: x.r.Method, Path: x.r.URL.Path, Status: x.status,
		Latency: g.opts.Now().Sub(x.start), ReqBytes: int64(len(x.body)), RespBytes: x.respBytes,
		TokensIn: x.reported.InputTokens, TokensOut: x.reported.OutputTokens, TokensCached: x.reported.CachedTokens,
		TokensCacheWrite: x.reported.CacheWriteTokens, TokensAudioIn: x.reported.AudioInputTokens, TokensAudioOut: x.reported.AudioOutputTokens,
		Priority: x.priority,
	}
	if len(e.Path) > maxOperationPath {
		e.Path = e.Path[:maxOperationPath]
	}
	if x.route.Upstream != nil {
		e.UpstreamID = x.route.Upstream.ID
	}
	// A model is the app's to name, so only a plain id is kept.
	if m := cmp.Or(x.reported.Model, x.model); safeModel.MatchString(m) {
		e.Model = m
	}
	if x.refused != "" {
		e.BlockedCode = string(x.refused)
		if x.status == 0 {
			e.Status = meanings[x.refused].status
		}
		// A request that reached for the upstream and found none is still a forwarded
		// request, answered by kman.
		if x.refused != codeUpstreamUnavailable {
			e.Kind = events.KindBlocked
		}
	}
	if e.Kind != events.KindBlocked && x.res != nil && !x.charge.Unknown {
		e.CostKnown, e.CostMicro, e.CostEstimated = true, x.charge.Micro, x.charge.Estimated
	}
	e.Country, e.ASN = x.geo.Country, int64(x.geo.ASN)
	g.opts.Events.Emit(e)
}
