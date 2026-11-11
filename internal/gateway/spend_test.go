package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/registry"
)

// The prices of these tests: an input token costs 1 µ$ and an output token 2 µ$,
// at most 1000 of them out. A request of 400 bytes reserves 100 + 2000.
const (
	reservation = 2100
	chatPath    = "/v1/chat/completions"
	askedFor    = `{"model":"m","messages":[{"role":"user","content":"` // + padding, so the body is 400 bytes
)

func chatBody() string {
	const tail = `"}]}`
	return askedFor + strings.Repeat("x", 400-len(askedFor)-len(tail)) + tail
}

func priceTable(t testing.TB) *pricing.Prices {
	t.Helper()
	entry := pricing.Entry{Mode: "chat", MaxOutput: 1000, Schedule: pricing.Schedule{Rates: pricing.Rates{Input: 1_000_000, Output: 2_000_000}}}
	tbl, err := pricing.New(pricing.Data{Schema: pricing.Schema, GeneratedAt: t0, Global: map[string]pricing.Entry{"m": entry, "m-2": entry}})
	if err != nil {
		t.Fatal(err)
	}
	return pricing.NewPrices(tbl)
}

// spending is a gateway with a meter, in front of handler, with one key under
// the policy given.
type spending struct {
	*stack
	meter  *meter.Meter
	secret string
	key    string
	clock  *atomic.Int64 // the gateway's time, as Unix nanoseconds, which a test may move while requests run
}

func spendingBehind(t *testing.T, handler http.Handler, pol string, edit ...func(*meter.Options)) *spending {
	t.Helper()
	o := meter.Options{Prices: priceTable(t), Counters: meter.NewMemory(), Now: func() time.Time { return t0 }}
	for _, f := range edit {
		f(&o)
	}
	m := meter.New(o)
	clock := &atomic.Int64{}
	clock.Store(t0.UnixNano())
	s, _ := behind(t, handler, func(g *gateway.Options) {
		g.Meter = m
		g.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	})
	spec := specOfRoute(t, s)
	spec.Profile, spec.InjectUsage = registry.ProfileOpenAI, true
	k, secret := newKey(t)
	k.Policy = pol
	s.publish(spec, k)
	return &spending{stack: s, meter: m, secret: secret, key: k.ID, clock: clock}
}

func (s *spending) spent() meter.Period { return s.meter.Spend(s.key, t0).Day }

func (s *spending) chat(body string) (*http.Response, string) {
	s.t.Helper()
	return s.send("POST", chatPath, body, withBearer(s.secret))
}

// reporting is an upstream that remembers its requests and answers with usage.
type reporting struct{ *recording }

func (r reporting) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.recording.got <- seen{method: req.Method, uri: req.RequestURI}
	io.Copy(io.Discard, req.Body)
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, usageResponse)
}

const usageResponse = `{"model":"m","usage":{"prompt_tokens":50,"completion_tokens":10}}` // 50 + 20

func TestARequestOnAMeteredUpstreamIsChargedForTheUsageItReports(t *testing.T) {
	s := spendingBehind(t, answering("application/json", usageResponse), `{}`)
	if resp, _ := s.chat(chatBody()); resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := s.spent(); got.CostMicro != 70 || got.Requests != 1 || got.ReservedMicro != 0 {
		t.Errorf("spend = %+v, want one request of 70", got)
	}
}

// endWatcher is the app's side of a response, which notes what the key's spend
// holds at the write that completes the body: an app that has the whole of a
// response may send its next request that instant.
type endWatcher struct {
	header        http.Header
	total         int
	written       int
	reservedAtEnd int64
	ended         bool
	reserved      func() int64
}

func (w *endWatcher) Header() http.Header { return w.header }
func (w *endWatcher) WriteHeader(int)     {}
func (w *endWatcher) Flush()              {}
func (w *endWatcher) Write(p []byte) (int, error) {
	w.written += len(p)
	if w.written == w.total && !w.ended {
		w.ended, w.reservedAtEnd = true, w.reserved()
	}
	return len(p), nil
}

// A request's reservation is settled before the last byte of a response that has
// a length goes out, so an app that sends its next request the moment it has the
// whole answer never meets the last one's reservation (SPEC §1 inv. 4). A response
// with no length ends when the handler returns, which is after the settle.
func TestAResponseWithALengthIsNotCompleteBeforeItsRequestIsSettled(t *testing.T) {
	const errorBody = `{"error":{"message":"overloaded"}}`
	for name, tc := range map[string]struct {
		up   http.Handler
		body string
	}{
		"an answer": {answering("application/json", usageResponse), usageResponse},
		"an error": {http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			io.WriteString(w, errorBody)
		}), errorBody},
	} {
		t.Run(name, func(t *testing.T) {
			s := spendingBehind(t, tc.up, `{}`)
			w := &endWatcher{header: http.Header{}, total: len(tc.body), reserved: func() int64 { return s.spent().ReservedMicro }}
			r := httptest.NewRequestWithContext(t.Context(), "POST", chatPath, strings.NewReader(chatBody()))
			r.Header = withBearer(s.secret)
			s.gw.ServeHTTP(w, r)
			if w.header.Get("Content-Length") == "" {
				t.Fatalf("the response has no Content-Length, so this test shows nothing: %v", w.header)
			}
			if !w.ended {
				t.Fatalf("the app was given %d of the %d bytes", w.written, w.total)
			}
			if w.reservedAtEnd != 0 {
				t.Errorf("the reservation of %d was still held when the response's last byte went out", w.reservedAtEnd)
			}
		})
	}
}

// F1: a body with no top-level model, on a key with a cap.
func TestRegression_F1_ACappedKeyCannotSkipItsCapBySendingNoModel(t *testing.T) {
	up := newRecording()
	s := spendingBehind(t, up, `{"daily_cap_micro":1000000}`)
	for name, tc := range map[string]struct{ method, path, body string }{
		"a body with no model":       {"POST", chatPath, `{"messages":[]}`},
		"the model nested":           {"POST", chatPath, `{"input":{"model":"m"}}`},
		"a model that isn't a name":  {"POST", chatPath, `{"model":["m"]}`},
		"a multipart body":           {"POST", "/v1/audio/transcriptions", "--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nm\r\n--b--"},
		"no body at all":             {"GET", "/v1/models", ""},
		"the model in the query":     {"POST", chatPath + "?model=m", `{}`},
		"a model in the wrong case":  {"POST", chatPath, `{"Model":"m"}`},
		"a model written with a key": {"POST", chatPath, `[{"model":"m"}]`},
	} {
		resp, body := s.send(tc.method, tc.path, tc.body, withBearer(s.secret))
		t.Run(name, func(t *testing.T) { wantRefusal(t, resp, body, 403, "unmetered_request") })
	}
	up.none(t)
	if got := s.spent(); got.Unmetered != 0 || got.CostMicro != 0 {
		t.Errorf("a refusal was counted as spend: %+v", got)
	}
}

func TestAnUnpricedModelIsRefusedOnACappedKeyWithTheCommandThatFixesIt(t *testing.T) {
	up := newRecording()
	s := spendingBehind(t, up, `{"monthly_cap_micro":1000000}`)
	resp, body := s.chat(`{"model":"gpt-9"}`)
	wantRefusal(t, resp, body, 403, "unknown_model_price")
	var e openAIError
	json.Unmarshal([]byte(body), &e)
	for _, want := range []string{"gpt-9", "mock", "kman price set gpt-9 --upstream mock --input", "--output", "--max-output"} {
		if !strings.Contains(e.Error.Message, want) {
			t.Errorf("the message %q lacks %q", e.Error.Message, want)
		}
	}
	up.none(t)
}

// What an app wrote is not echoed into a message just because it is a model name.
func TestAModelNameThatIsNotAModelNameIsNotEchoedBack(t *testing.T) {
	s := spendingBehind(t, newRecording(), `{"daily_cap_micro":1000000}`)
	resp, body := s.chat(`{"model":"x\"; rm -rf / #"}`)
	wantRefusal(t, resp, body, 403, "unknown_model_price")
	if strings.Contains(body, "rm -rf") {
		t.Errorf("the response repeats the app's model name: %s", body)
	}
}

func TestAKeyWithNoCapForwardsAnUnmeteredRequestAndCountsIt(t *testing.T) {
	up := newRecording()
	s := spendingBehind(t, up, `{}`)
	resp, _ := s.send("GET", "/v1/models", "", withBearer(s.secret))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	up.next(t)
	if got := s.spent(); got.Unmetered != 1 || got.CostMicro != 0 || got.Requests != 0 {
		t.Errorf("spend = %+v, want one unmetered request and no cost", got)
	}
}

// F2: a flood of 429s and 5xx from the upstream must not use up a cap.
func TestRegression_F2_UpstreamErrorsDoNotChargeTheCap(t *testing.T) {
	var calls atomic.Int64
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader([]int{429, 500, 503, 529}[calls.Add(1)%4])
		io.WriteString(w, `{"error":{"message":"overloaded"}}`)
	})
	s := spendingBehind(t, up, `{"daily_cap_micro":3000}`) // room for one reservation
	for i := range 40 {
		resp, body := s.chat(chatBody())
		if resp.StatusCode == 403 {
			t.Fatalf("request %d was refused for a cap after only upstream errors: %s", i, body)
		}
	}
	if got := s.spent(); got.CostMicro != 0 || got.ReservedMicro != 0 {
		t.Errorf("spend = %+v after only errors", got)
	}
}

func TestACapIsEnforcedBeforeTheRequestReachesTheUpstream(t *testing.T) {
	up := newRecording()
	s := spendingBehind(t, reporting{up}, `{"daily_cap_micro":2200}`)
	// A first request fits (2100 ≤ 2200) and its 70 is committed.
	if resp, body := s.chat(chatBody()); resp.StatusCode != 200 {
		t.Fatalf("first = %d %s", resp.StatusCode, body)
	}
	up.next(t)
	// 70 + 2100 ≤ 2200 still fits: and then 140 + 2100 does not.
	if resp, body := s.chat(chatBody()); resp.StatusCode != 200 {
		t.Fatalf("second = %d %s", resp.StatusCode, body)
	}
	up.next(t)
	resp, body := s.chat(chatBody())
	wantRefusal(t, resp, body, 403, "spend_cap_reached")
	up.none(t)
}

func TestRegression_F3_ACapAddedAfterSpendingCountsWhatWasSpent(t *testing.T) { // F3, through the gateway
	s := spendingBehind(t, answering("application/json", usageResponse), `{}`)
	for range 3 {
		s.chat(chatBody())
	}
	k, _ := s.routes.Lookup(s.key)
	capped := *k.Key
	capped.Policy = `{"daily_cap_micro":2200}`
	s.publish(specOfRoute(t, s.stack), capped)
	resp, body := s.chat(chatBody()) // 210 spent, and 2100 more doesn't fit in 2200
	wantRefusal(t, resp, body, 403, "spend_cap_reached")
}

func TestAKeyOnAGenericUpstreamWithACapIsRefusedAndOneWithoutIsForwarded(t *testing.T) {
	up := newRecording()
	s := spendingBehind(t, up, `{"daily_cap_micro":1000000}`)
	spec := specOfRoute(t, s.stack)
	spec.Profile = registry.ProfileGeneric
	k, _ := s.routes.Lookup(s.key)
	s.publish(spec, *k.Key)
	resp, body := s.chat(chatBody())
	wantRefusal(t, resp, body, 403, "unmetered_request")
	up.none(t)

	plain, secret := newKey(t)
	s.publish(spec, plain)
	if resp, _ := s.send("POST", chatPath, chatBody(), withBearer(secret)); resp.StatusCode != 200 {
		t.Errorf("a key with no cap on a generic upstream: %d", resp.StatusCode)
	}
	up.next(t)
	if got := s.meter.Spend(plain.ID, t0).Day; got.Unmetered != 0 || got.CostMicro != 0 {
		t.Errorf("a generic upstream's key has spend: %+v", got)
	}
}

// The decision made for #41: a 2xx that reports nothing ran, and is charged as an estimate.
func TestASuccessThatReportsNoUsageIsChargedTheReservationAsAnEstimate(t *testing.T) {
	s := spendingBehind(t, answering("application/json", `{"id":"x"}`), `{}`)
	s.chat(chatBody())
	if got := s.spent(); got.CostMicro != reservation || got.EstimatedMicro != reservation {
		t.Errorf("spend = %+v, want %d estimated", got, reservation)
	}
	// Except on a route that is free.
	s2 := spendingBehind(t, answering("application/json", `{"input_tokens":7}`), `{}`)
	s2.send("POST", "/v1/responses/input_tokens", chatBody(), withBearer(s2.secret))
	if got := s2.spent(); got.CostMicro != 0 {
		t.Errorf("a free route was charged: %+v", got)
	}
}

func TestAnUpstreamThatDidNotAnswerCostsNothingAndReleasesTheReservation(t *testing.T) {
	s := spendingBehind(t, http.NotFoundHandler(), `{"daily_cap_micro":3000}`)
	spec := specOf(t, closedAddr(t)) // nothing listens
	spec.Profile = registry.ProfileOpenAI
	k, _ := s.routes.Lookup(s.key)
	s.publish(spec, *k.Key)
	for range 5 {
		if resp, body := s.chat(chatBody()); resp.StatusCode != 502 {
			t.Fatalf("status = %d %s", resp.StatusCode, body)
		}
	}
	if got := s.spent(); got.CostMicro != 0 || got.ReservedMicro != 0 {
		t.Errorf("spend = %+v, the reservations were left held", got)
	}
}

// The failure test: kill the upstream mid-stream. The app's connection is cut,
// and the key is charged the whole reservation, as an estimate.
func TestAStreamKilledMidwayChargesTheWholeReservation(t *testing.T) {
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"model":"m","choices":[{"delta":{"content":"a"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // the upstream dies
	})
	s := spendingBehind(t, up, `{"daily_cap_micro":100000}`)
	req, _ := http.NewRequestWithContext(t.Context(), "POST", s.front.URL+chatPath, strings.NewReader(`{"model":"m","stream":true,"x":"`+strings.Repeat("y", 380)+`"}`))
	req.Header = withBearer(s.secret)
	resp, err := client().Do(req)
	if err == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if err == nil {
		t.Error("the app read a cut stream to its end as if it were whole")
	}
	s.front.Close() // waits for the handler, and so for settle
	got := s.spent()
	if got.CostMicro == 0 || got.CostMicro != got.EstimatedMicro || got.ReservedMicro != 0 {
		t.Errorf("spend = %+v, want the whole reservation, estimated, none left held", got)
	}
}

func TestAnAppThatGoesAwayMidStreamIsChargedTheWholeReservation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"model":"m","choices":[{"delta":{"content":"a"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	s := spendingBehind(t, up, `{}`)
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, "POST", s.front.URL+chatPath, strings.NewReader(`{"model":"m","stream":true,"x":"`+strings.Repeat("y", 380)+`"}`))
	req.Header = withBearer(s.secret)
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	resp.Body.Close()
	close(release)
	s.front.Close()
	if got := s.spent(); got.CostMicro == 0 || got.CostMicro != got.EstimatedMicro || got.ReservedMicro != 0 {
		t.Errorf("spend = %+v", got)
	}
}

func TestAKeyWhoseSpendCannotBeRecordedForAMinuteIsRefusedWithStoreUnavailable(t *testing.T) {
	down := &lockedCounters{Memory: meter.NewMemory()}
	s := spendingBehind(t, answering("application/json", usageResponse), `{"daily_cap_micro":1000000}`, func(o *meter.Options) { o.Counters = down })
	s.chat(chatBody())
	down.down.Store(true)
	s.meter.Flush(t.Context(), t0)
	s.clock.Store(t0.Add(30 * time.Second).UnixNano())
	if resp, _ := s.chat(chatBody()); resp.StatusCode != 200 {
		t.Fatalf("after 30 s: %d", resp.StatusCode)
	}
	s.clock.Store(t0.Add(61 * time.Second).UnixNano())
	resp, body := s.chat(chatBody())
	wantRefusal(t, resp, body, 503, "store_unavailable")
}

type lockedCounters struct {
	*meter.Memory
	down atomic.Bool
}

func (l *lockedCounters) Add(ctx context.Context, rows []meter.Row) error {
	if l.down.Load() {
		return errors.New("locked")
	}
	return l.Memory.Add(ctx, rows)
}

// A policy kman can't read must not be taken for none, which would free the key of its cap.
// The policy stage refuses such a key first (it allows nothing).
func TestAKeyWhosePolicyCannotBeReadIsRefused(t *testing.T) {
	up := newRecording()
	s := spendingBehind(t, up, `{"cap_from_a_later_kman":1}`)
	resp, body := s.chat(chatBody())
	wantRefusal(t, resp, body, 403, "path_not_allowed")
	up.none(t)
}

// With no meter at all, a key that needs one is refused, not let through.
func TestAKeyWithACapIsRefusedWhenThereIsNoMeter(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up)
	spec := specOfRoute(t, s)
	spec.Profile = registry.ProfileOpenAI
	k, secret := newKey(t)
	k.Policy = `{"daily_cap_micro":5}`
	s.publish(spec, k)
	resp, body := s.send("POST", chatPath, chatBody(), withBearer(secret))
	wantRefusal(t, resp, body, 503, "store_unavailable")
	up.none(t)
}
