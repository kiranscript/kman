package e2e_test

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

var metricsListening = regexp.MustCompile(`msg="metrics listening" addr=(\S+)`)

// metricsAddr waits for kman serve to say where it serves unauthenticated metrics.
func (s *server) metricsAddr(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if m := metricsListening.FindStringSubmatch(s.logs.String()); m != nil {
			return m[1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("kman serve never reported a metrics listener:\n%s", s.logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// scrape fetches /metrics from addr, with a bearer token when one is given.
func scrape(t *testing.T, addr, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+addr+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// eventually scrapes until the metrics match re, which events reach a moment
// after the response, and returns what it found.
func eventually(t *testing.T, addr, token string, re *regexp.Regexp) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, body := scrape(t, addr, token)
		if re.MatchString(body) {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("the metrics never matched %s:\n%s", re, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMetricsAreServedBehindTheAdminTokenAndOnTheOptInListener(t *testing.T) {
	h := installed(t)
	h.appendConfig(t, "metrics_listen: 127.0.0.1:0\n")
	s := h.serve(t)
	h.login(t, s)
	mock := newMock(t, "openai-chat")
	h.addUpstream(t, "mock", mock.host, mock.port, "--profile", "openai", "--inject-usage")
	p := &proxied{t: t, h: h, s: s, mock: mock, secret: h.createKey(t, "my-app", "mock")}
	p.setPrices("gpt-4o-mini", "gpt-4o-mini-2024-07-18")
	token := h.savedToken(t)
	open := s.metricsAddr(t)

	// The admin listener needs the admin token; the opt-in listener needs nothing.
	if code, body := scrape(t, s.admin, ""); code != 401 || strings.Contains(body, "kman_") {
		t.Errorf("admin /metrics with no token = %d:\n%s", code, body)
	}
	if code, _ := scrape(t, s.admin, "kma_nottherealone"); code != 401 {
		t.Errorf("admin /metrics with a bad token = %d, want 401", code)
	}
	code, body := scrape(t, s.admin, token)
	if code != 200 || !regexp.MustCompile(`(?m)^kman_price_data_age_seconds [0-9.e+]+$`).MatchString(body) {
		t.Errorf("admin /metrics with the token = %d:\n%s", code, body)
	}
	if code, body := scrape(t, open, ""); code != 200 || !strings.Contains(body, "kman_price_data_age_seconds") {
		t.Errorf("the metrics listener = %d:\n%s", code, body)
	}
	if code := get(t, open, "/admin/v1/whoami"); code != 404 {
		t.Errorf("the metrics listener serves /admin/v1/whoami = %d, want 404: it serves metrics only", code)
	}

	// One request of each kind, then what the metrics and the store say.
	if resp, b := p.chat(chatRequest); resp.StatusCode != 200 {
		t.Fatalf("chat = %d %s", resp.StatusCode, b)
	}
	resp, b := sendThrough(t, s, "POST", "/v1/chat/completions", strings.NewReader(chatRequest), http.Header{"Authorization": {"Bearer kmv_nope"}})
	wantError(t, resp, b, 401, "invalid_key")
	body = eventually(t, open, "", regexp.MustCompile(`kman_blocked_requests_total\{code="invalid_key",key_id=""\} 1`))
	for _, want := range []string{
		`kman_requests_total{key_id="`, `status_class="2xx",upstream_id="`,
		`kman_tokens_total{key_id="`, `kman_cost_usd_total{key_id="`, "} 2.2e-05",
		"kman_events_dropped_total", "kman_events_written_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the metrics lack %q:\n%s", want, body)
		}
	}
	for _, leak := range []string{p.secret, sentinel, "Say hello"} {
		if strings.Contains(body, leak) {
			t.Errorf("the metrics contain %q", leak)
		}
	}

	// The events are in the store once kman stops, and usage_hourly is counted.
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Skip("cannot signal a process here:", err)
	}
	if code := s.wait(t); code != 0 {
		t.Fatalf("kman serve exited %d:\n%s", code, s.logs)
	}
	st, err := store.Open(context.Background(), store.Options{Dir: h.dataDir(), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var ev []store.EventRow
	var use []store.UsageRow
	if err := st.Do(context.Background(), func(tx *store.Tx) (err error) {
		if ev, err = tx.EventsSince(time.Now().Add(-time.Hour)); err != nil {
			return err
		}
		use, err = tx.UsageSince(time.Now().Add(-time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(ev) != 2 {
		t.Fatalf("the store holds %d events, want 2: %+v", len(ev), ev)
	}
	var req, blocked store.EventRow
	for _, e := range ev {
		if e.Kind == "request" {
			req = e
		} else {
			blocked = e
		}
	}
	if req.Status != 200 || req.Model == "" || req.Operation != "POST /v1/chat/completions" || req.TokensIn == 0 ||
		!req.CostKnown || req.CostMicro != 22 || req.ClientIP != "127.0.0.1" || req.UpstreamID == "" || req.KeyID == "" {
		t.Errorf("the request event: %+v", req)
	}
	if blocked.BlockedCode != "invalid_key" || blocked.Status != 401 || blocked.KeyID != "" {
		t.Errorf("the blocked event: %+v", blocked)
	}
	if len(use) != 1 || use[0].KeyID != req.KeyID || use[0].Requests != 1 || use[0].CostMicro != 22 {
		t.Errorf("usage_hourly: %+v", use)
	}
}

func TestThereIsNoMetricsListenerUnlessConfigured(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(s.logs.String(), "metrics listening") {
		t.Errorf("a metrics listener started with no metrics_listen:\n%s", s.logs)
	}
	if code, _ := scrape(t, s.admin, ""); code != 401 {
		t.Errorf("admin /metrics with no token = %d, want 401", code)
	}
}
