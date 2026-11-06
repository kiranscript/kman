package gateway

import (
	"crypto/x509"
	"log/slog"
	"net/http"
	"sync"
	"time"

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

	// RootCAs is who an upstream's certificate must chain to, and nil means the
	// system's. Tests set it to trust their own upstream.
	RootCAs *x509.CertPool
	// Transport replaces the real one. It is for tests.
	Transport http.RoundTripper
}

// Gateway is the proxy listener's http.Handler.
type Gateway struct {
	opts  Options
	base  *http.Transport
	named sync.Map // TLS server name → *http.Transport
}

// New returns a Gateway.
func New(opts Options) *Gateway {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	return &Gateway{opts: opts, base: newTransport(opts.RootCAs, "")}
}

// exchange is one request on its way through the pipeline.
type exchange struct {
	w     http.ResponseWriter
	r     *http.Request
	route registry.Route
	body  []byte
	// abort means the response can't be finished properly: the connection is cut,
	// so the app can't take what it got for the whole of it.
	abort bool
}

// refusal is why a stage stopped a request, before anything of a response was
// written. An empty message means the code's usual one.
type refusal struct {
	code    code
	message string
}

// stage is a step of the pipeline. A refusal ends the request.
type stage func(*Gateway, *exchange) *refusal

// pipeline is the request path up to the upstream's response, in order
// (SPEC §2). The body is read only once a request has a good key, and before
// policy, which reads the model from it.
var pipeline = [...]stage{
	(*Gateway).authenticate,
	(*Gateway).buffer,
	(*Gateway).policy,
	(*Gateway).limit,
	(*Gateway).reserve,
	(*Gateway).forward,
}

// ServeHTTP runs one request through the pipeline.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x := exchange{w: w, r: r}
	for _, run := range pipeline {
		if ref := run(g, &x); ref != nil {
			if !x.abort {
				writeError(w, ref.code, ref.message)
			}
			break
		}
	}
	g.settle(&x)
	g.emit(&x)
	if x.abort {
		panic(http.ErrAbortHandler)
	}
}

// policy will hold a request to its key's policy (#42): allowlists and locks.
func (g *Gateway) policy(*exchange) *refusal { return nil }

// limit will hold a key to its RPM limit (#43).
func (g *Gateway) limit(*exchange) *refusal { return nil }

// reserve will take a reservation against the key's caps (#40, #41). Pricing
// goes by the upstream's ID, Preset and PriceMultiplierPPM, never its Name, which
// the operator chooses.
func (g *Gateway) reserve(*exchange) *refusal { return nil }

// settle will charge what the upstream reported, or release the reservation
// (#41). It runs for every request, refused or not.
func (g *Gateway) settle(*exchange) {}

// emit will publish the request's event (#46). It runs for every request.
func (g *Gateway) emit(*exchange) {}
