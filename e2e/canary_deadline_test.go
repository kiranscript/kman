package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The canary deadline gate (SPEC §17, M2 done criteria): a canary's alert, and a
// disabled key's, reach a channel within 5 s under 256-way load with another
// channel hung. It is a job of its own in CI, and runs with the rest of go test.
//
// It is an e2e test, and not a unit test of the alerts module, because it is about
// real queues, a real SQLite and a real clock: the real binary serves 256 clients
// at once, each sending requests through it as fast as it can, which fan out to
// the store writer (the one writer of the one database the priority lane needs as
// well), the metrics, and the alerts; a channel never answers, and another does.
// The unit tests of the alerts module show the pipeline's logic on a fake clock.
const (
	gateClients  = 256
	gateDeadline = 5 * time.Second
)

func TestCanaryDeadlineGate(t *testing.T) {
	// An upstream that answers at once, so the load is kman's: 256 requests in flight.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(upstream.Close)

	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	host, portText, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	h.addUpstream(t, "load", host, port)
	loadKey := h.createKey(t, "load", "load")
	victimSecret := h.createKey(t, "victim", "load")
	h.mustRun(t, "key", "disable", "victim")
	victim := h.showKey(t, "victim")

	hung, ok := newSink(t, 0), newSink(t, 200)
	h.addWebhook(t, "hung", hung)
	h.addWebhook(t, "pager", ok)
	out, _ := h.mustRun(t, "canary", "create", "decoy", "--planted", "the deploy repo's .env", "--json")
	var made struct {
		Canary struct{ ID string }
		Secret string
	}
	if err := json.Unmarshal([]byte(out), &made); err != nil || made.Secret == "" {
		t.Fatalf("canary create --json printed %q: %v", out, err)
	}
	// The setup alerts (a key disabled, a canary made) are not the ones measured.
	setup := len(ok.received())

	// 256 clients, each sending requests through kman for as long as the test runs.
	var sent atomic.Int64
	ctx, stop := context.WithCancel(t.Context())
	client := &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: gateClients, MaxConnsPerHost: gateClients}}
	var load sync.WaitGroup
	for range gateClients {
		load.Go(func() {
			for ctx.Err() == nil {
				req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+s.proxy+"/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
				req.Header.Set("Authorization", "Bearer "+loadKey)
				if resp, err := client.Do(req); err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					sent.Add(1)
				}
			}
		})
	}
	t.Cleanup(func() { stop(); load.Wait() })
	time.Sleep(time.Second) // let the load build

	use := func(secret string) {
		req, _ := http.NewRequestWithContext(t.Context(), "POST", "http://"+s.proxy+"/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	start := time.Now()
	use(made.Secret)
	use(victimSecret)

	var sawCanary, sawDisabled time.Duration
	for deadline := start.Add(gateDeadline + 5*time.Second); (sawCanary == 0 || sawDisabled == 0) && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		for _, r := range ok.received()[setup:] {
			switch {
			case sawCanary == 0 && strings.Contains(r.body, `"kind":"canary"`) && strings.Contains(r.body, made.Canary.ID):
				sawCanary = r.at.Sub(start)
			case sawDisabled == 0 && strings.Contains(r.body, `"kind":"disabled_key"`) && strings.Contains(r.body, victim.ID):
				sawDisabled = r.at.Sub(start)
			}
		}
	}
	t.Logf("canary alert after %v, disabled-key alert after %v, with %d requests sent by %d clients and a channel hung", sawCanary, sawDisabled, sent.Load(), gateClients)
	if sawCanary <= 0 || sawCanary > gateDeadline {
		t.Errorf("the canary alert arrived after %v (0 means never), want within %v", sawCanary, gateDeadline)
	}
	if sawDisabled <= 0 || sawDisabled > gateDeadline {
		t.Errorf("the disabled-key alert arrived after %v (0 means never), want within %v", sawDisabled, gateDeadline)
	}
}
