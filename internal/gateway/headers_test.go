package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/registry"
)

func TestHeadersAnUpstreamMustNotSeeAreStripped(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)
	req := httptest.NewRequest("POST", "/v1/things", strings.NewReader("{}"))
	req.Header = http.Header{
		"Authorization": {"Bearer " + secret},
		// Hop by hop, including one that Connection names.
		"Connection":          {"keep-alive, X-Named-Hop", "X-Second-Hop"},
		"X-Named-Hop":         {"1"},
		"X-Second-Hop":        {"1"},
		"Keep-Alive":          {"timeout=5"},
		"Proxy-Authenticate":  {"Basic"},
		"Proxy-Authorization": {"Basic abc"},
		"Proxy-Connection":    {"keep-alive"},
		"Te":                  {"trailers"},
		"Trailer":             {"X-Later"},
		"Transfer-Encoding":   {"chunked"},
		"Upgrade":             {"websocket"},
		"Expect":              {"100-continue"},
		// Inbound auth an SDK may send beside the key.
		"Api-Key":        {"sk-from-the-sdk"},
		"X-Goog-Api-Key": {"sk-from-the-sdk"},
		"X-Api-Key":      {"sk-from-the-sdk"},
		// Forwarding headers, added by nothing and passed on by nothing.
		"Forwarded":          {"for=10.0.0.1"},
		"Via":                {"1.1 inner"},
		"X-Forwarded-For":    {"10.0.0.1"},
		"X-Forwarded-Host":   {"inner"},
		"X-Forwarded-Port":   {"443"},
		"X-Forwarded-Proto":  {"https"},
		"X-Forwarded-Server": {"inner"},
		"X-Real-Ip":          {"10.0.0.1"},
		// What the upstream is meant to read.
		"Accept":            {"application/json"},
		"Accept-Encoding":   {"gzip"},
		"Anthropic-Version": {"2023-06-01"},
		"Content-Type":      {"application/json"},
		"Openai-Beta":       {"assistants=v2"},
		"User-Agent":        {"my-app/1.0"},
		"X-Multi":           {"a", "b"},
	}
	if resp := s.direct(req); resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := up.next(t)
	// Exactly what's left: this also catches anything added.
	want := map[string]string{
		"Authorization":     "Bearer " + credential,
		"Accept":            "application/json",
		"Accept-Encoding":   "gzip",
		"Anthropic-Version": "2023-06-01",
		"Content-Length":    "2",
		"Content-Type":      "application/json",
		"Openai-Beta":       "assistants=v2",
		"User-Agent":        "my-app/1.0",
		"X-Multi":           "a",
	}
	for name, values := range got.header {
		if v, ok := want[name]; !ok || values[0] != v {
			t.Errorf("the upstream saw %s: %q", name, values)
		}
	}
	for name := range want {
		if got.header.Get(name) == "" {
			t.Errorf("the upstream did not see %s", name)
		}
	}
	if m := got.header.Values("X-Multi"); len(m) != 2 || m[1] != "b" {
		t.Errorf("X-Multi = %q", m)
	}
}

// kman adds nothing of its own, not even the HTTP library's defaults.
func TestKmanAddsNoHeadersOfItsOwn(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)
	req := httptest.NewRequest("GET", "/v1/things", nil)
	req.Header = withBearer(secret)
	s.direct(req)
	got := up.next(t)
	for name, values := range got.header {
		if name != "Authorization" {
			t.Errorf("the upstream saw %s: %q", name, values)
		}
	}
}

func TestTheUpstreamsAuthHeaderTakesTheCredentialWhateverItsName(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)
	k, _ := s.routes.Lookup(mustID(t, secret))
	spec := specOfRoute(t, s)
	spec.Auth = registry.Auth{Style: registry.AuthHeader, Header: "X-Postmark-Server-Token: {key}"}
	s.publish(spec, *k.Key)

	s.send("GET", "/", "", http.Header{"X-Postmark-Server-Token": {secret}})
	got := up.next(t)
	if v := got.header.Values("X-Postmark-Server-Token"); len(v) != 1 || v[0] != credential {
		t.Errorf("X-Postmark-Server-Token = %q", v)
	}
	if v := got.header.Get("Authorization"); v != "" {
		t.Errorf("Authorization = %q", v)
	}
}

func TestPathsAndQueryStringsPassThroughUnchanged(t *testing.T) {
	for _, uri := range []string{
		"/",
		"/v1/things",
		"/v1/things?a=1",
		"/v1/a%2Fb/c?x=1&x=2&empty=&y=%20+z;odd=%zz&",
		"/v1/things?",
		"/v1/UPPER%3alower/",
		"//double//slash",
		"/v1/../up",
	} {
		t.Run(uri, func(t *testing.T) {
			up := newRecording()
			s, secret := behind(t, up)
			// A raw request: a URL parser in the test's client would tidy these.
			req := httptest.NewRequest("GET", uri, nil)
			req.Header = withBearer(secret)
			s.direct(req)
			if got := up.next(t); got.uri != uri {
				t.Errorf("the upstream got %q, want %q", got.uri, uri)
			}
		})
	}
}

// A proxy-style request names another host. kman holds a credential for one
// upstream, and goes nowhere else.
func TestARequestTargetNamingAnotherHostGoesToTheUpstream(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)
	req := httptest.NewRequest("GET", "http://elsewhere.example/v1/things?a=1", nil)
	req.Header = withBearer(secret)
	if resp := s.direct(req); resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := up.next(t)
	if got.uri != "/v1/things?a=1" {
		t.Errorf("the upstream got %q", got.uri)
	}
	spec := specOfRoute(t, s)
	if !strings.HasPrefix(got.host, spec.Host+":") {
		t.Errorf("Host = %q, want the upstream's", got.host)
	}
}

// TRACE would echo the credential back to the app, and the others have no path
// to forward.
func TestMethodsThatCannotBeForwardedSafelyAreRefused(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{"TRACE", "/v1/things"},
		{"CONNECT", "/v1/things"},
		{"OPTIONS", "*"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			up := newRecording()
			s, secret := behind(t, up)
			req := httptest.NewRequest("GET", "/", nil)
			req.Method = tc.method
			req.URL.Path = tc.target
			req.Header = withBearer(secret)
			resp := s.direct(req)
			body, _ := io.ReadAll(resp.Body)
			wantRefusal(t, resp, string(body), 403, "path_not_allowed")
			up.none(t)
		})
	}
}

func TestHopByHopHeadersOfTheResponseAreStripped(t *testing.T) {
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Connection", "X-Named-Hop")
		h.Set("X-Named-Hop", "1")
		h.Set("Keep-Alive", "timeout=5")
		h.Set("Proxy-Authenticate", "Basic")
		h.Set("Upgrade", "websocket")
		h.Set("X-Request-Id", "abc")
		h.Add("Set-Cookie", "a=1")
		h.Add("Set-Cookie", "b=2")
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header = withBearer(secret)
	resp := s.direct(req)
	for _, name := range []string{"Connection", "X-Named-Hop", "Keep-Alive", "Proxy-Authenticate", "Upgrade"} {
		if v := resp.Header.Get(name); v != "" {
			t.Errorf("the app saw %s: %q", name, v)
		}
	}
	if resp.Header.Get("X-Request-Id") != "abc" || len(resp.Header.Values("Set-Cookie")) != 2 {
		t.Errorf("headers = %v", resp.Header)
	}
}

// The response is the upstream's, and net/http must not guess at a type for it.
func TestAResponseWithNoContentTypeGetsNone(t *testing.T) {
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header()["Content-Type"] = nil
		io.WriteString(w, "<html>not really</html>")
	}))
	resp, _ := s.send("GET", "/", "", withBearer(secret))
	if v, ok := resp.Header["Content-Type"]; ok {
		t.Errorf("Content-Type = %q", v)
	}
}

// captureTransport remembers the outbound request and answers empty.
type captureTransport struct{ got *http.Request }

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.got = r
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
}

// The URL names the port only when it isn't the scheme's own, so the Host header
// reads as an SDK talking to the upstream directly would send it.
func TestTheOutboundAddressComesFromTheUpstream(t *testing.T) {
	for _, tc := range []struct {
		name       string
		spec       registry.Spec
		scheme     string
		host, want string
	}{
		{"TLS on 443", registry.Spec{Host: "api.example.com", Port: 443, TLS: true}, "https", "api.example.com", ""},
		{"TLS elsewhere", registry.Spec{Host: "api.example.com", Port: 8443, TLS: true}, "https", "api.example.com:8443", ""},
		{"plain on 80", registry.Spec{Host: "api.example.com", Port: 80}, "http", "api.example.com", ""},
		{"plain on 443", registry.Spec{Host: "api.example.com", Port: 443}, "http", "api.example.com:443", ""},
		{"an IPv6 address", registry.Spec{Host: "::1", Port: 8443, TLS: true}, "https", "[::1]:8443", ""},
		{"a server name", registry.Spec{Host: "10.0.0.5", Port: 443, TLS: true, ServerName: "api.internal"}, "https", "10.0.0.5", "api.internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &captureTransport{}
			s := newStack(t, func(o *gateway.Options) { o.Transport = capture })
			k, secret := newKey(t)
			tc.spec.Profile, tc.spec.Auth = registry.ProfileGeneric, bearer
			s.publish(tc.spec, k)
			s.send("GET", "/x", "", withBearer(secret))
			got := capture.got
			if got == nil {
				t.Fatal("nothing went out")
			}
			if got.URL.Scheme != tc.scheme || got.URL.Host != tc.host {
				t.Errorf("URL = %s://%s, want %s://%s", got.URL.Scheme, got.URL.Host, tc.scheme, tc.host)
			}
			if got.Host != tc.want {
				t.Errorf("Host = %q, want %q", got.Host, tc.want)
			}
		})
	}
}

func mustID(t *testing.T, secret string) string {
	t.Helper()
	return secret[len("kmn_"):][:8]
}
