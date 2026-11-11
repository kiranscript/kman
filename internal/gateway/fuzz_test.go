package gateway_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/gateway"
)

// FuzzAKeyGoesNoFurtherThanKman sends a working key however the fuzzer likes,
// beside another header the fuzzer picks. Whatever arrives must not hold the key.
func FuzzAKeyGoesNoFurtherThanKman(f *testing.F) {
	f.Add("Authorization", "Bearer {key}", "X-Other", "value")
	f.Add("X-Api-Key", "{key}", "Authorization", "Bearer sk-real")
	f.Add("X-Thing", "bearer   {key}", "X-Other", "{key}")
	f.Add("Connection", "Authorization", "Authorization", "{key}")
	f.Add("x-lower-case", "{key}", "Te", "trailers")
	f.Add("Authorization", "Basic {basic}", "X-Other", "")
	f.Add("Authorization", "Basic !!!", "Authorization", "Token {key}")
	capture := &captureTransport{}
	s := newStack(f, func(o *gateway.Options) { o.Transport = capture })
	k, secret := newKey(f)
	s.publish(specOf(f, "http://127.0.0.1:1"), k)
	basic := base64.StdEncoding.EncodeToString([]byte(secret + ":"))
	fill := strings.NewReplacer("{key}", secret, "{basic}", basic)
	f.Fuzz(func(t *testing.T, name1, value1, name2, value2 string) {
		capture.got = nil
		req := httptest.NewRequest("GET", "/", nil)
		req.Header = http.Header{}
		req.Header.Add(name1, fill.Replace(value1))
		req.Header.Add(name2, fill.Replace(value2))
		resp := s.direct(req)
		if resp.StatusCode != 200 {
			if capture.got != nil {
				t.Errorf("refused with %d after forwarding", resp.StatusCode)
			}
			return
		}
		// A header that only mentions the key, such as "note: {key}", is the app's
		// to send. One holding it as a key is not.
		for header, values := range capture.got.Header {
			for _, v := range values {
				if strings.HasPrefix(strings.TrimSpace(v), secret) {
					t.Errorf("%s reached the upstream holding the key: %q", header, v)
				}
			}
		}
		if v := capture.got.Header.Values("Authorization"); len(v) != 1 || v[0] != "Bearer "+credential {
			t.Errorf("Authorization = %q", v)
		}
	})
}

// locationTransport answers every request with a redirect to its location.
type locationTransport struct{ location string }

func (l *locationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 302, Header: http.Header{"Location": {l.location}}, Body: http.NoBody, Request: r}, nil
}

// FuzzARedirectNeverLeavesKman holds any Location to one rule: what an app is told
// either stays on kman, however a lenient client reads it, or isn't told at all.
func FuzzARedirectNeverLeavesKman(f *testing.F) {
	for _, seed := range []string{
		"/ok", "ok", "https://api.example.com/x", "https://api.example.com//evil.example", "https://evil.example/x",
		"//evil.example", "///evil.example", `/\evil.example`, `\\evil.example`, "https:evil.example", "https:/evil.example",
		"https://api.example.com@evil.example/", "https://api.example.com:8443/", "javascript:alert(1)", "?a=1", "#f", "",
	} {
		f.Add(seed)
	}
	answer := &locationTransport{}
	s := newStack(f, func(o *gateway.Options) { o.Transport = answer })
	k, secret := newKey(f)
	s.publish(specOf(f, "https://api.example.com:443"), k)
	f.Fuzz(func(t *testing.T, location string) {
		answer.location = location
		req := httptest.NewRequest("GET", "/", nil)
		req.Header = withBearer(secret)
		told := s.direct(req).Header.Get("Location")
		if told == "" {
			return
		}
		if strings.Contains(told, `\`) || strings.HasPrefix(told, "//") {
			t.Fatalf("Location %q became %q", location, told)
		}
		u, err := url.Parse(told)
		if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
			t.Errorf("Location %q became %q, which can leave kman (%v)", location, told, err)
		}
	})
}
