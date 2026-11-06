package gateway_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/token"
	"github.com/kirank55/kman/internal/vault"
)

// cannedTransport answers at once, so a benchmark sees kman's overhead and no
// network: the number SPEC §17's targets are about, and steady enough for the gate.
type cannedTransport struct{ body []byte }

func (c cannedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	io.Copy(io.Discard, r.Body)
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(c.body)),
		Request:    r,
	}, nil
}

// discardWriter is a ResponseWriter that costs nothing, with a Flush as the real
// one has.
type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardWriter) WriteHeader(int)             {}
func (d *discardWriter) Flush()                      {}

// benchGateway is a gateway with one working key, over a canned upstream, and a
// request for it to serve.
func benchGateway(b *testing.B) (*gateway.Gateway, string) {
	b.Helper()
	v, _, err := vault.Create(masterKey{}, "dek_test", vault.Options{AllowDebug: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(v.Close)
	sealed, err := v.Seal(credID, []byte(credential))
	if err != nil {
		b.Fatal(err)
	}
	if err := v.Put(credID, sealed); err != nil {
		b.Fatal(err)
	}
	m, err := token.MintVirtualKey(token.NewKeyID())
	if err != nil {
		b.Fatal(err)
	}
	reg := registry.New()
	reg.Apply(registry.Change{
		Generation: 1,
		Upstreams: []registry.Upstream{{ID: upstreamID, Name: "mock", Spec: registry.Spec{
			Profile: registry.ProfileGeneric, Host: "api.example.com", Port: 443, TLS: true, Paths: []string{"* /**"}, Auth: bearer,
		}, Generation: 1}},
		Credentials: []registry.Credential{{ID: credID, UpstreamID: upstreamID, Generation: 1}},
		Keys: []registry.Key{{ID: string(m.ID), CredentialID: credID, Status: "active",
			Secrets: []registry.KeySecret{{Hash: m.Hash}}, ExpiresAt: t0.Add(time.Hour), Generation: 1}},
	})
	return gateway.New(gateway.Options{
		Routes:    reg,
		Vault:     v,
		Now:       func() time.Time { return t0 },
		Log:       slog.New(slog.DiscardHandler),
		Transport: cannedTransport{body: []byte(strings.Repeat("r", 1024))},
	}), m.Reveal()
}

func benchRequest(b *testing.B, secret string, body *bytes.Reader) *http.Request {
	b.Helper()
	req, err := http.NewRequestWithContext(b.Context(), "POST", "http://127.0.0.1:8080/v1/chat/completions?a=1", body)
	if err != nil {
		b.Fatal(err)
	}
	req.Header = http.Header{
		"Authorization": {"Bearer " + secret},
		"Content-Type":  {"application/json"},
		"User-Agent":    {"bench/1.0"},
		"Accept":        {"application/json"},
	}
	return req
}

// BenchmarkGateway is kman's cost of one request: authenticate, buffer 1 KiB,
// forward with the credential, and pass 1 KiB back.
func BenchmarkGateway(b *testing.B) {
	gw, secret := benchGateway(b)
	payload := []byte(strings.Repeat("q", 1024))
	body := bytes.NewReader(payload)
	req := benchRequest(b, secret, body)
	w := &discardWriter{h: http.Header{}}
	b.ReportAllocs()
	for b.Loop() {
		body.Reset(payload)
		req.Body = io.NopCloser(body)
		clear(w.h)
		gw.ServeHTTP(w, req)
	}
}

// BenchmarkGatewayConcurrent is the same under load: the request path takes no
// locks, so it should scale with the cores (SPEC §1, invariant 5).
func BenchmarkGatewayConcurrent(b *testing.B) {
	gw, secret := benchGateway(b)
	payload := []byte(strings.Repeat("q", 1024))
	b.ReportAllocs()
	b.SetParallelism(8)
	b.RunParallel(func(pb *testing.PB) {
		body := bytes.NewReader(payload)
		req := benchRequest(b, secret, body)
		w := &discardWriter{h: http.Header{}}
		for pb.Next() {
			body.Reset(payload)
			req.Body = io.NopCloser(body)
			clear(w.h)
			gw.ServeHTTP(w, req)
		}
	})
}

// BenchmarkBadKey is what a flood of made-up keys costs: it must stay cheap, since
// nothing has authenticated.
func BenchmarkBadKey(b *testing.B) {
	gw, secret := benchGateway(b)
	req := benchRequest(b, secret[:len(secret)-1]+"!", bytes.NewReader(nil))
	req.Body = http.NoBody
	w := &discardWriter{h: http.Header{}}
	b.ReportAllocs()
	for b.Loop() {
		clear(w.h)
		gw.ServeHTTP(w, req)
	}
}
