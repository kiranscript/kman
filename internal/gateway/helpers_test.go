package gateway_test

import (
	"bytes"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/token"
	"github.com/kirank55/kman/internal/vault"
)

const (
	credential = "sk-real-credential-0123456789"
	credID     = "cr_test"
	upstreamID = "up_test"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// masterKey is a key source holding a fixed master key.
type masterKey struct{}

func (masterKey) MasterKey() ([]byte, error) { return make([]byte, vault.KeySize), nil }
func (masterKey) String() string             { return "the test's master key" }

// countingRoutes is the real registry, counting how often it is asked.
type countingRoutes struct {
	*registry.Registry
	lookups atomic.Int64
}

func (c *countingRoutes) Lookup(id string) (registry.Route, bool) {
	c.lookups.Add(1)
	return c.Registry.Lookup(id)
}

// stack is a gateway over a real registry and vault, serving on a real listener,
// in front of a real upstream unless a test swaps the transport.
type stack struct {
	t      testing.TB
	routes *countingRoutes
	vault  *vault.Vault
	gen    int64
	now    time.Time
	logs   *bytes.Buffer
	opts   gateway.Options
	gw     *gateway.Gateway
	front  *httptest.Server
}

// newStack is a stack with no keys yet. edit changes the gateway's options.
func newStack(t testing.TB, edit ...func(*gateway.Options)) *stack {
	t.Helper()
	v, _, err := vault.Create(masterKey{}, "dek_test", vault.Options{AllowDebug: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	sealed, err := v.Seal(credID, []byte(credential))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Put(credID, sealed); err != nil {
		t.Fatal(err)
	}
	s := &stack{t: t, routes: &countingRoutes{Registry: registry.New()}, vault: v, now: t0, logs: &bytes.Buffer{}}
	s.opts = gateway.Options{
		Routes: s.routes,
		Vault:  v,
		Now:    func() time.Time { return s.now },
		Log:    slog.New(slog.NewTextHandler(s.logs, nil)),
	}
	for _, f := range edit {
		f(&s.opts)
	}
	s.gw = gateway.New(s.opts)
	s.front = httptest.NewServer(s.gw)
	t.Cleanup(s.front.Close)
	return s
}

// bearer is the auth most upstreams ask for.
var bearer = registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"}

// specOf is a Spec reaching addr, a test server's URL.
func specOf(t testing.TB, addr string) registry.Spec {
	t.Helper()
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return registry.Spec{Profile: registry.ProfileGeneric, Host: host, Port: n, TLS: u.Scheme == "https", Paths: []string{"* /**"}, Auth: bearer}
}

// publish puts the upstream and its credential in the registry, as admin does.
func (s *stack) publish(spec registry.Spec, keys ...registry.Key) {
	s.gen++
	s.routes.Apply(registry.Change{
		Generation:  s.gen,
		Upstreams:   []registry.Upstream{{ID: upstreamID, Name: "mock", Spec: spec, Generation: s.gen}},
		Credentials: []registry.Credential{{ID: credID, UpstreamID: upstreamID, Name: "main", Generation: s.gen}},
		Keys:        keys,
	})
}

// newKey is a working virtual key on the credential, and its secret.
func newKey(t testing.TB) (registry.Key, string) {
	t.Helper()
	m, err := token.MintVirtualKey(token.NewKeyID())
	if err != nil {
		t.Fatal(err)
	}
	k := registry.Key{ID: string(m.ID), CredentialID: credID, Status: "active",
		Secrets: []registry.KeySecret{{Hash: m.Hash}}, ExpiresAt: t0.Add(time.Hour),
		Policy: `{}`, PolicyVersion: 1} // what admin publishes for a key with no policy
	return k, m.Reveal()
}

// newKeyWithID is another secret for a key id: its hash and the token.
func newKeyWithID(t *testing.T, id string) (token.Hash, string) {
	t.Helper()
	m, err := token.MintVirtualKey(token.KeyID(id))
	if err != nil {
		t.Fatal(err)
	}
	return m.Hash, m.Reveal()
}

// specOfRoute is the Spec behind() published, to publish keys beside it.
func specOfRoute(t *testing.T, s *stack) registry.Spec {
	t.Helper()
	u, ok := s.routes.Upstream(upstreamID)
	if !ok {
		t.Fatal("no upstream published")
	}
	return u.Spec
}

// behind is a stack in front of handler, with one working key, and the key's
// secret.
func behind(t *testing.T, handler http.Handler, edit ...func(*gateway.Options)) (*stack, string) {
	t.Helper()
	up := httptest.NewServer(handler)
	t.Cleanup(up.Close)
	s := newStack(t, edit...)
	k, secret := newKey(t)
	s.publish(specOf(t, up.URL), k)
	return s, secret
}

// seen is what an upstream received.
type seen struct {
	method, uri string
	host        string
	header      http.Header
	body        string
}

// recording is an upstream that remembers the requests it gets and answers 200.
type recording struct {
	got chan seen
}

func newRecording() *recording { return &recording{got: make(chan seen, 16)} }

func (r *recording) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.got <- seen{req.Method, req.RequestURI, req.Host, req.Header.Clone(), string(body)}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"ok":true}`)
}

// next is the request the upstream received, failing if there was none.
func (r *recording) next(t *testing.T) seen {
	t.Helper()
	select {
	case s := <-r.got:
		return s
	default:
		t.Fatal("the upstream received nothing")
		return seen{}
	}
}

// none fails if the upstream received anything.
func (r *recording) none(t *testing.T) {
	t.Helper()
	select {
	case s := <-r.got:
		t.Errorf("the upstream received %s %s", s.method, s.uri)
	default:
	}
}

// client never follows a redirect, sends what the test sets, and adds nothing
// of its own beyond framing.
func client() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil, DisableCompression: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// send makes a request through the gateway and reads the whole response.
func (s *stack) send(method, uri, body string, header http.Header) (*http.Response, string) {
	s.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.front.URL+uri, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp, string(b)
}

func withBearer(secret string) http.Header {
	return http.Header{"Authorization": {"Bearer " + secret}}
}

// poolOf trusts one test server's certificate.
func poolOf(srv *httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}

// direct runs a request made by hand through the handler, for what a client
// library would not send: a request-target in absolute form, a Trailer header.
func (s *stack) direct(req *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	s.gw.ServeHTTP(rec, req)
	return rec.Result()
}
