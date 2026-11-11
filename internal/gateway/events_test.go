package gateway_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/registry"
)

// watched is a bus with one subscriber, running until the test ends.
type watched struct {
	bus *events.Bus
	sub *events.Subscription
}

func watch(t *testing.T, o events.Options) *watched {
	t.Helper()
	b := events.New(o)
	w := &watched{bus: b, sub: b.Subscribe("test", events.SubOptions{Buffer: 256})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return w
}

// next is the next event, failing if none comes.
func (w *watched) next(t *testing.T) events.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case e, ok := <-w.sub.C:
		if !ok {
			t.Fatal("the subscription closed")
		}
		return e
	case <-ctx.Done():
		t.Fatal("no event was emitted")
	}
	panic("unreachable")
}

// clocked is a stack whose time a test moves, with a meter on the openai
// profile and one key under the policy given.
func eventsBehind(t *testing.T, w *watched, handler http.Handler, pol string, withMeter bool) (*stack, string, string, *atomic.Int64) {
	t.Helper()
	clock := &atomic.Int64{}
	clock.Store(t0.UnixNano())
	s, _ := behind(t, handler, func(g *gateway.Options) {
		g.Events = w.bus
		g.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
		if withMeter {
			g.Meter = meter.New(meter.Options{Prices: priceTable(t), Counters: meter.NewMemory(), Now: g.Now})
		}
	})
	spec := specOfRoute(t, s)
	spec.Profile, spec.InjectUsage = registry.ProfileOpenAI, true
	k, secret := newKey(t)
	k.Policy = pol
	s.publish(spec, k)
	return s, secret, k.ID, clock
}

func TestAForwardedRequestEmitsOneEventWithItsFacts(t *testing.T) {
	w := watch(t, events.Options{})
	var clock *atomic.Int64
	up := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		clock.Add(int64(250 * time.Millisecond))
		rw.Header().Set("Content-Type", "application/json")
		io.WriteString(rw, usageResponse)
	})
	s, secret, keyID, c := eventsBehind(t, w, up, `{}`, true)
	clock = c
	body := chatBody()
	resp, got := s.send("POST", chatPath+"?api-key=in-the-query", body, withBearer(secret))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	e := w.next(t)
	if e.Kind != events.KindRequest || e.KeyID != keyID || e.UpstreamID != upstreamID || e.Model != "m" {
		t.Errorf("event %+v: wrong kind, key, upstream or model", e)
	}
	if e.Operation() != "POST "+chatPath {
		t.Errorf("operation %q, want the method and path with no query", e.Operation())
	}
	if e.Status != 200 || e.BlockedCode != "" || e.Latency != 250*time.Millisecond || !e.Time.Equal(t0) {
		t.Errorf("event %+v: wrong status, latency or time", e)
	}
	if e.ReqBytes != int64(len(body)) || e.RespBytes != int64(len(got)) {
		t.Errorf("bytes %d in, %d out; want %d and %d", e.ReqBytes, e.RespBytes, len(body), len(got))
	}
	if e.TokensIn != 50 || e.TokensOut != 10 || !e.CostKnown || e.CostMicro != 70 || e.CostEstimated {
		t.Errorf("event %+v: wrong tokens or cost, want 50 in, 10 out, 70 µ$ exactly", e)
	}
	if !e.ClientIP.IsValid() || !e.ClientIP.IsLoopback() {
		t.Errorf("client IP %v, want the loopback the test came from", e.ClientIP)
	}
	// One event per request: the next event is the next request's.
	s.send("GET", "/v1/models", "", withBearer(secret))
	if e := w.next(t); e.Operation() != "GET /v1/models" {
		t.Errorf("the next event is %q, want the second request's: the first made more than one", e.Operation())
	}
}

func TestAnEstimatedCostIsFlagged(t *testing.T) {
	w := watch(t, events.Options{})
	// A 2xx that reports no usage is charged the reservation as an estimate.
	s, secret, _, _ := eventsBehind(t, w, answering("application/json", `{}`), `{}`, true)
	s.send("POST", chatPath, chatBody(), withBearer(secret))
	e := w.next(t)
	if !e.CostKnown || !e.CostEstimated || e.CostMicro != reservation {
		t.Errorf("event %+v, want an estimated cost of %d", e, reservation)
	}
}

func TestACostThatCannotBeKnownIsMarkedUnknownNotZero(t *testing.T) {
	cases := map[string]struct {
		meter bool
		body  string
	}{
		"no meter":              {false, chatBody()},
		"a model with no price": {true, strings.Replace(chatBody(), `"m"`, `"no-such-model"`, 1)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := watch(t, events.Options{})
			s, secret, _, _ := eventsBehind(t, w, answering("application/json", `{"ok":true}`), `{}`, c.meter)
			s.send("POST", chatPath, c.body, withBearer(secret))
			if e := w.next(t); e.Kind != events.KindRequest || e.CostKnown {
				t.Errorf("event %+v, want a forwarded request with an unknown cost", e)
			}
		})
	}
}

func TestARefusedRequestEmitsABlockedEvent(t *testing.T) {
	w := watch(t, events.Options{})
	s, _, _, _ := eventsBehind(t, w, answering("application/json", `{}`), `{}`, true)
	resp, _ := s.send("POST", chatPath, `{"model":"m"}`, http.Header{"Authorization": {"Bearer kmv_nope"}})
	if resp.StatusCode != 401 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	e := w.next(t)
	if e.Kind != events.KindBlocked || e.BlockedCode != "invalid_key" || e.Status != 401 || e.KeyID != "" || e.Priority {
		t.Errorf("event %+v, want a blocked invalid_key 401 with no key, in the ordinary lane", e)
	}
	if e.CostKnown {
		t.Errorf("a refused request has no cost: %+v", e)
	}
}

func TestAPolicyRefusalNamesTheKeyAndItsCode(t *testing.T) {
	w := watch(t, events.Options{})
	s, secret, keyID, _ := eventsBehind(t, w, answering("application/json", `{}`), `{"allowed_models":["only-this"]}`, true)
	resp, _ := s.send("POST", chatPath, chatBody(), withBearer(secret))
	if resp.StatusCode != 403 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	e := w.next(t)
	if e.Kind != events.KindBlocked || e.BlockedCode != "model_not_allowed" || e.KeyID != keyID || e.UpstreamID != upstreamID || e.Priority {
		t.Errorf("event %+v", e)
	}
}

func TestADisabledKeyEventNamesTheKeyAndTakesThePriorityLane(t *testing.T) {
	w := watch(t, events.Options{})
	s, secret, keyID, _ := eventsBehind(t, w, answering("application/json", `{}`), `{}`, false)
	spec := specOfRoute(t, s)
	k, ok := s.routes.Lookup(keyID)
	if !ok {
		t.Fatal("no key")
	}
	disabled := *k.Key
	disabled.Status = "disabled"
	s.publish(spec, disabled)
	resp, _ := s.send("POST", chatPath, chatBody(), withBearer(secret))
	if resp.StatusCode != 403 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	e := w.next(t)
	if e.Kind != events.KindBlocked || e.BlockedCode != "key_disabled" || e.KeyID != keyID || !e.Priority {
		t.Errorf("event %+v, want a priority blocked key_disabled event for the key", e)
	}
}

func TestACanaryEventNamesTheKeyAndTakesThePriorityLane(t *testing.T) {
	w := watch(t, events.Options{})
	s := newStack(t, func(g *gateway.Options) { g.Events = w.bus })
	canary, secret := newKey(t)
	canary.CredentialID = "" // a canary has no credential
	s.publish(specOf(t, "http://127.0.0.1:1"), canary)
	resp, _ := s.send("GET", "/v1/models", "", withBearer(secret))
	if resp.StatusCode != 401 {
		t.Fatalf("status %d, a canary answers like any bad key", resp.StatusCode)
	}
	e := w.next(t)
	if e.KeyID != canary.ID || !e.Priority || e.Kind != events.KindBlocked {
		t.Errorf("event %+v, want a priority event naming the canary", e)
	}
}

func TestAnUnreachableUpstreamIsAForwardedRequestAnswered502(t *testing.T) {
	w := watch(t, events.Options{})
	s := newStack(t, func(g *gateway.Options) { g.Events = w.bus })
	k, secret := newKey(t)
	s.publish(specOf(t, "http://127.0.0.1:1"), k) // nothing listens there
	resp, _ := s.send("GET", "/v1/models", "", withBearer(secret))
	if resp.StatusCode != 502 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	e := w.next(t)
	if e.Kind != events.KindRequest || e.Status != 502 || e.BlockedCode != "upstream_unavailable" || e.KeyID != k.ID {
		t.Errorf("event %+v", e)
	}
}

// SPEC §9 and invariant 3: an event never holds request or response content, or a secret.
func TestAnEventHoldsNoContentAndNoSecret(t *testing.T) {
	w := watch(t, events.Options{})
	up := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("X-Secret-Header", "RESPONSE-HEADER-SENTINEL")
		io.WriteString(rw, `{"model":"m","choices":[{"message":{"content":"RESPONSE-SENTINEL"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	})
	s, secret, _, _ := eventsBehind(t, w, up, `{}`, true)
	h := withBearer(secret)
	h.Set("X-Request-Header", "REQUEST-HEADER-SENTINEL")
	body := `{"model":"m","messages":[{"role":"user","content":"REQUEST-SENTINEL"}]}`
	s.send("POST", chatPath+"?q=QUERY-SENTINEL", body, h)
	e := w.next(t)
	dump := fmt.Sprintf("%+v %#v", e, e)
	for _, leak := range []string{"SENTINEL", secret, credential} {
		if strings.Contains(dump, leak) {
			t.Errorf("the event holds %q: %s", leak, dump)
		}
	}
}

func TestAnAppChosenModelNameIsNotCarriedUnlessItIsPlain(t *testing.T) {
	w := watch(t, events.Options{})
	s, secret, _, _ := eventsBehind(t, w, answering("application/json", `{}`), `{}`, true)
	s.send("POST", chatPath, `{"model":"evil\n\"model with spaces & content\""}`, withBearer(secret))
	if e := w.next(t); e.Model != "" {
		t.Errorf("model %q, want none for a name that is not a plain id", e.Model)
	}
}

// SPEC invariant 5 and the request path: a full queue never makes a request wait,
// and what was dropped can be seen.
func TestAFullEventQueueNeverBlocksTheRequestPathAndTheDropsAreVisible(t *testing.T) {
	b := events.New(events.Options{Capacity: 2, PriorityCapacity: 2}) // never run: nothing leaves the queue
	s, _ := behind(t, answering("application/json", `{}`), func(g *gateway.Options) { g.Events = b })
	k, sec := newKey(t)
	s.publish(specOfRoute(t, s), k)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			resp, _ := s.send("GET", "/v1/models", "", withBearer(sec))
			if resp.StatusCode != 200 {
				t.Errorf("status %d, want the request served though the queue is full", resp.StatusCode)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("requests blocked on a full event queue")
	}
	if st := b.Stats(); st.Queued != 2 || st.Dropped != 18 {
		t.Errorf("stats %+v, want 2 queued and 18 dropped", st)
	}
}

func TestAGatewayWithNoEventBusStillServes(t *testing.T) {
	s, secret := behind(t, answering("application/json", `{}`))
	if resp, _ := s.send("GET", "/v1/models", "", withBearer(secret)); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
