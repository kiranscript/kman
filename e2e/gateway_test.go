package e2e_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestARequestThroughKmanReachesTheUpstreamWithTheCredential(t *testing.T) {
	p := behindKman(t, "generic-json")

	const uri = "/v1/things?a=1&a=2&odd=%20+x;y"
	resp, body := p.send("POST", uri, strings.NewReader(`{"name":"x"}`))

	if resp.StatusCode != 201 || body != "{\"id\":\"thing_1\",\"ok\":true}\n" {
		t.Errorf("response = %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Request-Id") != "req_123" || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("response headers = %v", resp.Header)
	}
	if v := resp.Header.Get("X-Kman-Error"); v != "" {
		t.Errorf("X-Kman-Error = %q", v)
	}
	got := p.mock.only(t)
	if got.Method != "POST" || got.URI != uri || got.Body != `{"name":"x"}` {
		t.Errorf("the upstream got %s %s %q", got.Method, got.URI, got.Body)
	}
	if v := got.Header.Values("Authorization"); len(v) != 1 || v[0] != "Bearer "+sentinel {
		t.Errorf("Authorization = %q", v)
	}
	for name, values := range got.Header {
		for _, v := range values {
			if strings.Contains(v, p.secret) {
				t.Errorf("%s reached the upstream holding the virtual key", name)
			}
		}
		switch name {
		case "Forwarded", "Via", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip":
			t.Errorf("kman added %s: %q", name, values)
		}
	}
}

func TestAStreamPassesThroughTheRealBinary(t *testing.T) {
	p := behindKman(t, "generic-stream")
	resp, body := p.send("POST", "/v1/stream", strings.NewReader("{}"))
	want := "data: {\"n\":1}\n\ndata: {\"n\":2}\n\ndata: [DONE]\n\n"
	if resp.StatusCode != 200 || body != want || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("response = %d %v %q", resp.StatusCode, resp.Header, body)
	}
}

func TestABadChecksumNeverReachesTheUpstream(t *testing.T) {
	p := behindKman(t, "")
	last := "0"
	if strings.HasSuffix(p.secret, last) {
		last = "1"
	}
	bad := p.secret[:len(p.secret)-1] + last
	resp, body := sendThrough(t, p.s, "GET", "/v1/things", nil, http.Header{"Authorization": {"Bearer " + bad}})
	wantError(t, resp, body, 401, "invalid_key")

	resp, body = sendThrough(t, p.s, "GET", "/v1/things", nil, nil)
	wantError(t, resp, body, 401, "invalid_key")

	if got := p.mock.requests(); len(got) != 0 {
		t.Errorf("the upstream got %+v", got)
	}
}

func TestAKeyStillWorksAfterARestart(t *testing.T) {
	p := behindKman(t, "generic-json")
	stopServer(t, p.s)
	s := p.h.serve(t)
	resp, body := sendThrough(t, s, "POST", "/v1/things", strings.NewReader("{}"), http.Header{"Authorization": {"Bearer " + p.secret}})
	if resp.StatusCode != 201 {
		t.Errorf("after a restart, response = %d %s", resp.StatusCode, body)
	}
	if got := p.mock.only(t); got.Header.Get("Authorization") != "Bearer "+sentinel {
		t.Errorf("after a restart, Authorization = %q", got.Header.Get("Authorization"))
	}
}

func TestABodyOverTheLimitIsRefusedByTheRealBinary(t *testing.T) {
	p := behindKman(t, "")
	const limit = 32 << 20
	resp, body := p.send("POST", "/v1/things", io.LimitReader(zeroes{}, limit+1))
	wantError(t, resp, body, 413, "request_too_large")
	if got := p.mock.requests(); len(got) != 0 {
		t.Errorf("the upstream got %d requests", len(got))
	}
}

type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestARedirectOffTheUpstreamIsBlocked(t *testing.T) {
	p := behindKman(t, "redirect-away")
	resp, body := p.send("GET", "/v1/things", nil)
	wantError(t, resp, body, 502, "upstream_unavailable")
	if v := resp.Header.Get("Location"); v != "" {
		t.Errorf("Location = %q", v)
	}
	if strings.Contains(body, "elsewhere.example") {
		t.Errorf("the body names the target: %s", body)
	}
	p.mock.only(t)
}

// closedPort is a port on loopback where nothing listens.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// When an upstream is down the logs say so, and hold no secret or query string.
func TestAnUpstreamThatIsDownIsA502AndLeaksNothing(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addUpstream(t, "down", "127.0.0.1", closedPort(t))
	secret := h.createKey(t, "my-app", "down")

	const query = "sekret=in-the-query"
	resp, body := sendThrough(t, s, "POST", "/v1/things?"+query, strings.NewReader("the-body"), http.Header{"Authorization": {"Bearer " + secret}})
	wantError(t, resp, body, 502, "upstream_unavailable")

	stopServer(t, s)
	logs := s.logs.String()
	if !strings.Contains(logs, "the upstream is unavailable") {
		t.Errorf("the log does not say the upstream is down:\n%s", logs)
	}
	for _, leak := range []string{query, secret, sentinel, "the-body"} {
		if strings.Contains(logs, leak) || strings.Contains(body, leak) {
			t.Errorf("%q leaked:\nlogs: %s\nbody: %s", leak, logs, body)
		}
	}
}

// An HTTP_PROXY in kman's environment must not get to see a credential. The
// upstream's name resolves nowhere, so the request can only fail, unless kman
// hands it to the proxy. Go ignores proxies for loopback addresses, so a real
// upstream on loopback couldn't tell.
func TestTheProxyInTheEnvironmentIsNotUsed(t *testing.T) {
	var hits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(proxy.Close)

	h := installed(t)
	envy := h.with("HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "https_proxy="+proxy.URL)
	s := envy.serve(t)
	envy.login(t, s)
	envy.addUpstream(t, "nowhere", "kman-e2e-no-such-host.invalid", 80)
	secret := envy.createKey(t, "my-app", "nowhere")

	resp, body := sendThrough(t, s, "GET", "/v1/things", nil, http.Header{"Authorization": {"Bearer " + secret}})
	wantError(t, resp, body, 502, "upstream_unavailable")
	if n := hits.Load(); n != 0 {
		t.Errorf("the proxy in the environment got %d requests", n)
	}
}
