package e2e_test

import (
	"strings"
	"testing"
	"time"
)

// thing is the request every lifecycle test makes of kman.
func (p *proxied) thing() (status int, errCode string, body string) {
	p.t.Helper()
	resp, body := p.send("POST", "/v1/things", strings.NewReader("{}"))
	return resp.StatusCode, resp.Header.Get("X-Kman-Error"), body
}

// Each of these is the very next request after the command returns: nothing
// waits, and the server is not restarted.
func TestDisablingAndRevokingAKeyStopTheNextRequest(t *testing.T) {
	p := behindKman(t, "generic-json-twice")
	if status, _, _ := p.thing(); status != 201 {
		t.Fatalf("a good key: %d", status)
	}

	p.h.mustRun(t, "key", "disable", "my-app")
	resp, body := p.send("POST", "/v1/things", strings.NewReader("{}"))
	wantError(t, resp, body, 403, "key_disabled")

	p.h.mustRun(t, "key", "enable", "my-app")
	if status, code, body := p.thing(); status != 201 || code != "" {
		t.Errorf("after enable: %d %q %s", status, code, body)
	}

	id := p.h.showKey(t, "my-app").ID
	p.h.mustRun(t, "key", "revoke", "my-app")
	resp, body = p.send("POST", "/v1/things", strings.NewReader("{}"))
	wantError(t, resp, body, 401, "key_revoked")

	// A revoked key stays revoked.
	if _, stderr, code := p.h.run(t, "key", "enable", id); code == 0 || !strings.Contains(stderr, "revoked") {
		t.Errorf("enabling a revoked key: exit %d, %s", code, stderr)
	}
	resp, body = p.send("POST", "/v1/things", strings.NewReader("{}"))
	wantError(t, resp, body, 401, "key_revoked")

	if got := len(p.mock.requests()); got != 2 {
		t.Errorf("the upstream got %d requests, want the 2 made with a working key", got)
	}
}

func TestAnExpiredKeyStopsTheNextRequest(t *testing.T) {
	p := behindKman(t, "generic-json")
	p.h.mustRun(t, "key", "update", "my-app", "--ttl", "3s")
	if status, _, _ := p.thing(); status != 201 {
		t.Fatalf("before its expiry: %d", status)
	}
	expires, err := time.Parse(time.RFC3339, p.h.showKey(t, "my-app").ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(expires) + 200*time.Millisecond)

	resp, body := p.send("POST", "/v1/things", strings.NewReader("{}"))
	wantError(t, resp, body, 401, "key_expired")
	if got := len(p.mock.requests()); got != 1 {
		t.Errorf("the upstream got %d requests, want 1", got)
	}
}

func TestNoKeyMayBeCreatedOrExtendedPastTheMaximumLifetime(t *testing.T) {
	p := behindKman(t, "")
	if _, stderr, code := p.h.run(t, "key", "create", "long", "--upstream", "mock", "--ttl", "400d"); code == 0 || !strings.Contains(stderr, "maximum key lifetime") {
		t.Errorf("create past the maximum: exit %d, %s", code, stderr)
	}
	if _, stderr, code := p.h.run(t, "key", "update", "my-app", "--ttl", "400d"); code == 0 || !strings.Contains(stderr, "maximum key lifetime") {
		t.Errorf("extend past the maximum: exit %d, %s", code, stderr)
	}
	if _, stderr, code := p.h.run(t, "key", "create", "forever", "--upstream", "mock", "--ttl", "never"); code == 0 || !strings.Contains(stderr, "never") {
		t.Errorf("never: exit %d, %s", code, stderr)
	}
	p.h.mustRun(t, "key", "update", "my-app", "--ttl", "364d")
}
