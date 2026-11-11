package e2e_test

import (
	"net/http"
	"strings"
	"testing"
)

func (p *proxied) setPolicy(policy string) {
	p.t.Helper()
	p.h.mustRun(p.t, "key", "update", "my-app", "--policy", policy)
}

func TestAKeysPathAllowlistAndRateLimitHoldThroughTheRealBinary(t *testing.T) {
	p := behindKman(t, "generic-json")
	p.setPolicy(`{"allowed_paths":["POST /v1/things"],"rpm_limit":1}`)

	resp, body := p.send("GET", "/v1/other", nil)
	wantError(t, resp, body, http.StatusForbidden, "path_not_allowed")
	resp, body = p.send("POST", "/v1/things/../other", strings.NewReader("{}"))
	wantError(t, resp, body, http.StatusForbidden, "path_not_allowed")

	if resp, body = p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`)); resp.StatusCode != 201 {
		t.Fatalf("the allowed request = %d %s", resp.StatusCode, body)
	}
	resp, body = p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`))
	wantError(t, resp, body, http.StatusTooManyRequests, "rate_limited")
	if resp.Header.Get("Retry-After") == "" {
		t.Error("a rate-limited response has no Retry-After")
	}
	if got := p.mock.only(t); got.URI != "/v1/things" {
		t.Errorf("the upstream got %s", got.URI)
	}
}

func TestAModelAllowlistHoldsThroughTheRealBinary(t *testing.T) {
	p := behindKman(t, "") // a mock that expects nothing
	p.setPolicy(`{"allowed_models":["gpt-4o"]}`)

	resp, body := p.send("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini"}`))
	wantError(t, resp, body, http.StatusForbidden, "model_not_allowed")
	resp, body = p.send("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","model":"gpt-4o-mini"}`))
	wantError(t, resp, body, http.StatusForbidden, "unmetered_request")
	resp, body = p.send("GET", "/v1/models", nil)
	wantError(t, resp, body, http.StatusForbidden, "unmetered_request")
	if n := len(p.mock.requests()); n != 0 {
		t.Errorf("the upstream got %d requests", n)
	}
}

func TestAnAddressSendingInvalidKeysIsThrottledThroughTheRealBinary(t *testing.T) {
	p := behindKman(t, "")
	header := http.Header{"Authorization": {"Bearer kmn_aaaaaaaa_" + strings.Repeat("b", 49)}}
	var last *http.Response
	var lastBody string
	for i := 0; i < 40; i++ {
		last, lastBody = sendThrough(t, p.s, "GET", "/v1/x", nil, header)
		if last.StatusCode == http.StatusTooManyRequests {
			break
		}
	}
	wantError(t, last, lastBody, http.StatusTooManyRequests, "rate_limited")
	resp, body := p.send("GET", "/v1/x", nil) // a good key from the same address
	wantError(t, resp, body, http.StatusTooManyRequests, "rate_limited")
}
