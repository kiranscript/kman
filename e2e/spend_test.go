package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The prices the spend tests set, so that what they expect doesn't move with the
// price data: an input token costs 1 µ$, an output token 2 µ$, and a response can
// run to 1000 of them. A reservation for a request of n bytes is ⌈n/4⌉ + 2000.
const reservedOutput = 2000

// spendOf is what kman key show --json says a key has spent.
type spendOf struct {
	Spend struct {
		Today, Month period
	} `json:"spend"`
	Policy json.RawMessage `json:"policy"`
}

type period struct {
	Period            string `json:"period"`
	CostMicro         int64  `json:"cost_micro"`
	EstimatedMicro    int64  `json:"estimated_micro"`
	ReservedMicro     int64  `json:"reserved_micro"`
	Requests          int64  `json:"requests"`
	UnmeteredRequests int64  `json:"unmetered_requests"`
	CapMicro          int64  `json:"cap_micro"`
}

func (p *proxied) setPrices(models ...string) {
	p.t.Helper()
	for _, m := range models {
		p.h.mustRun(p.t, "price", "set", m, "--upstream", "mock", "--input", "1", "--output", "2", "--max-output", "1000")
	}
}

func (p *proxied) spend() spendOf {
	p.t.Helper()
	out, _ := p.h.mustRun(p.t, "key", "show", "my-app", "--json")
	var got spendOf
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		p.t.Fatalf("key show --json printed %q: %v", out, err)
	}
	return got
}

// settled waits for the request just answered to be settled, which happens
// as its handler ends, a moment after the app has the last of the response.
func (p *proxied) settled(requests int64) spendOf {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := p.spend()
		if got.Spend.Today.Requests >= requests && got.Spend.Today.ReservedMicro == 0 {
			return got
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("the spend never settled: %+v", got.Spend.Today)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const chatRequest = `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello"}]}`

func reservationFor(body string) int64 { return int64((len(body)+3)/4) + reservedOutput }

func (p *proxied) chat(body string) (*http.Response, string) {
	p.t.Helper()
	return p.send("POST", "/v1/chat/completions", strings.NewReader(body))
}

func TestSpendIsMeteredAndShownBesideTheCaps(t *testing.T) {
	p := behindKmanAs(t, "openai-chat", "openai")
	p.setPrices("gpt-4o-mini", "gpt-4o-mini-2024-07-18")
	p.setPolicy(`{"daily_cap_micro":5000000,"monthly_cap_micro":50000000}`)

	resp, body := p.chat(chatRequest)
	if resp.StatusCode != 200 {
		t.Fatalf("response = %d %s", resp.StatusCode, body)
	}
	// 8 input + 4 cached (at the input price) + 5 output at 2 = 22 µ$.
	got := p.settled(1)
	if got.Spend.Today.CostMicro != 22 || got.Spend.Month.CostMicro != 22 || got.Spend.Today.EstimatedMicro != 0 ||
		got.Spend.Today.CapMicro != 5_000_000 || got.Spend.Month.CapMicro != 50_000_000 {
		t.Errorf("spend = %+v", got.Spend)
	}
	out, _ := p.h.mustRun(t, "key", "show", "my-app")
	if !strings.Contains(out, "spend today:") || !strings.Contains(out, "$0.000022 of $5 cap") || !strings.Contains(out, "spend month:") {
		t.Errorf("key show:\n%s", out)
	}
	list, _ := p.h.mustRun(t, "key", "list", "--json")
	if !strings.Contains(list, `"cost_micro": 22`) && !strings.Contains(list, `"cost_micro":22`) {
		t.Errorf("key list --json does not carry the spend:\n%s", list)
	}
}

// Spend reaches the store when kman stops, and is read back when it starts.
func TestSpendSurvivesARestart(t *testing.T) {
	p := behindKmanAs(t, "openai-chat", "openai")
	p.setPrices("gpt-4o-mini", "gpt-4o-mini-2024-07-18")
	p.chat(chatRequest)
	p.settled(1)
	if err := p.s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Skip("cannot signal a process here:", err)
	}
	if code := p.s.wait(t); code != 0 {
		t.Fatalf("kman serve exited %d:\n%s", code, p.s.logs)
	}
	s := p.h.serve(t)
	p.h.login(t, s)
	if got := p.spend(); got.Spend.Today.CostMicro != 22 || got.Spend.Month.Requests != 1 {
		t.Errorf("after a restart: %+v", got.Spend)
	}
}

// F1 through the real binary: no top-level model is not a way past a cap.
func TestRegression_F1_ANoModelRequestIsRefusedOnACappedKey(t *testing.T) {
	p := behindKmanAs(t, "", "openai")
	p.setPolicy(`{"daily_cap_micro":5000000}`)
	for _, body := range []string{`{"messages":[]}`, `[{"model":"gpt-4o-mini"}]`, `not json`} {
		resp, b := p.chat(body)
		wantError(t, resp, b, 403, "unmetered_request")
	}
	resp, b := p.send("GET", "/v1/models", nil)
	wantError(t, resp, b, 403, "unmetered_request")
	if n := len(p.mock.requests()); n != 0 {
		t.Errorf("the upstream got %d requests", n)
	}
}

func TestAnUnpricedModelIsRefusedWithTheCommandToPriceIt(t *testing.T) {
	p := behindKmanAs(t, "", "openai")
	p.setPolicy(`{"daily_cap_micro":5000000}`)
	resp, b := p.chat(`{"model":"gpt-unpriced-1"}`)
	wantError(t, resp, b, 403, "unknown_model_price")
	if !strings.Contains(b, "kman price set gpt-unpriced-1 --upstream mock") {
		t.Errorf("the refusal does not give the command: %s", b)
	}
}

// F2 through the real binary: a flood of 429s doesn't use up a cap.
func TestRegression_F2_UpstreamErrorsDoNotChargeACap(t *testing.T) {
	p := behindKmanAs(t, "openai-429", "openai")
	p.setPrices("gpt-4o-mini")
	p.setPolicy(`{"daily_cap_micro":2100}`) // room for one reservation, and no more
	for i := range 3 {
		if resp, b := p.chat(chatRequest); resp.StatusCode != 429 {
			t.Fatalf("request %d: %d %s", i, resp.StatusCode, b)
		}
	}
	if got := p.settled(0); got.Spend.Today.CostMicro != 0 || got.Spend.Today.ReservedMicro != 0 {
		t.Errorf("spend after three 429s = %+v", got.Spend.Today)
	}
}

// The decision made for #41: a 2xx with no usage is charged as an estimate, so that
// it runs through a cap and can't be a way round one.
func TestASuccessWithNoUsageIsChargedAsAnEstimateAndRunsDownTheCap(t *testing.T) {
	p := behindKmanAs(t, "openai-no-usage", "openai")
	p.setPrices("gpt-4o-mini")
	want := reservationFor(chatRequest)
	p.setPolicy(`{"daily_cap_micro":` + itoa(want+100) + `}`)
	if resp, b := p.chat(chatRequest); resp.StatusCode != 200 {
		t.Fatalf("first: %d %s", resp.StatusCode, b)
	}
	got := p.settled(1)
	if got.Spend.Today.CostMicro != want || got.Spend.Today.EstimatedMicro != want {
		t.Errorf("spend = %+v, want %d, estimated", got.Spend.Today, want)
	}
	resp, b := p.chat(chatRequest)
	wantError(t, resp, b, 403, "spend_cap_reached")
	if n := len(p.mock.requests()); n != 1 {
		t.Errorf("the upstream got %d requests, want 1", n)
	}
}

// The failure test of SPEC §17: the upstream dies mid-stream. The app's
// connection is cut, so it can't take the half for the whole, and the key is charged
// the whole reservation as an estimate.
func TestAStreamKilledMidwayCutsTheAppAndChargesTheReservation(t *testing.T) {
	p := behindKmanAs(t, "openai-killed", "openai")
	p.setPrices("gpt-4o-mini")
	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"Say hello"}]}`
	req, err := http.NewRequestWithContext(t.Context(), "POST", "http://"+p.s.proxy+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+p.secret)
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if err == nil {
		t.Error("the app read a cut stream to its end as if it were whole")
	}
	got := p.settled(1)
	want := reservationFor(body)
	if got.Spend.Today.CostMicro != want || got.Spend.Today.EstimatedMicro != want {
		t.Errorf("spend = %+v, want the whole reservation %d, estimated", got.Spend.Today, want)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
