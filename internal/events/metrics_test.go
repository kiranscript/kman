package events_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/kirank55/kman/internal/events"
)

// scrape runs the events through a Metrics and returns what /metrics says.
func scrape(t *testing.T, evs []events.Event, extra ...events.Metric) string {
	t.Helper()
	b := events.New(events.Options{})
	m := events.NewMetrics()
	sub := b.Subscribe("metrics", events.SubOptions{Buffer: 100})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { b.Run(ctx) })
	wg.Go(func() { m.Run(sub) })
	for _, e := range evs {
		b.Emit(e)
	}
	cancel()
	wg.Wait()
	rec := httptest.NewRecorder()
	m.Handler(extra...).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("Content-Type %q is not the Prometheus text format", ct)
	}
	return rec.Body.String()
}

func wantLine(t *testing.T, body, line string) {
	t.Helper()
	for l := range strings.SplitSeq(body, "\n") {
		if l == line {
			return
		}
	}
	t.Errorf("metrics lack the line %q; got:\n%s", line, body)
}

func TestMetricsCountEventsByKeyAndOutcome(t *testing.T) {
	body := scrape(t, []events.Event{
		{Kind: events.KindRequest, KeyID: "k1", UpstreamID: "up_1", Status: 200, TokensIn: 10, TokensOut: 20, TokensCached: 3, CostMicro: 1500000, CostKnown: true},
		{Kind: events.KindRequest, KeyID: "k1", UpstreamID: "up_1", Status: 200, TokensIn: 1, CostMicro: 500000, CostKnown: true},
		{Kind: events.KindRequest, KeyID: "k1", UpstreamID: "up_1", Status: 429},
		{Kind: events.KindRequest, KeyID: "k1", UpstreamID: "up_1", Status: 502, BlockedCode: "upstream_unavailable"},
		{Kind: events.KindBlocked, KeyID: "k1", Status: 403, BlockedCode: "key_disabled"},
		{Kind: events.KindBlocked, Status: 401, BlockedCode: "invalid_key"},
	})
	wantLine(t, body, `kman_requests_total{key_id="k1",status_class="2xx",upstream_id="up_1"} 2`)
	wantLine(t, body, `kman_requests_total{key_id="k1",status_class="4xx",upstream_id="up_1"} 1`)
	wantLine(t, body, `kman_requests_total{key_id="k1",status_class="5xx",upstream_id="up_1"} 1`)
	wantLine(t, body, `kman_blocked_requests_total{code="key_disabled",key_id="k1"} 1`)
	wantLine(t, body, `kman_blocked_requests_total{code="invalid_key",key_id=""} 1`)
	wantLine(t, body, `kman_tokens_total{key_id="k1",type="input"} 11`)
	wantLine(t, body, `kman_tokens_total{key_id="k1",type="output"} 20`)
	wantLine(t, body, `kman_tokens_total{key_id="k1",type="cached"} 3`)
	wantLine(t, body, `kman_cost_usd_total{key_id="k1"} 2`)
	wantLine(t, body, `kman_unpriced_requests_total{key_id="k1"} 2`)
	if !strings.Contains(body, "# TYPE kman_requests_total counter") || !strings.Contains(body, "# HELP kman_requests_total ") {
		t.Errorf("metrics lack HELP and TYPE lines:\n%s", body)
	}
}

func TestMetricsNeverCarryRequestContentOrAppChosenLabels(t *testing.T) {
	body := scrape(t, []events.Event{{
		Kind: events.KindRequest, KeyID: "k1", UpstreamID: "up_1", Status: 200, ClientIP: netip.MustParseAddr("203.0.113.9"),
		Model: "model-chosen-by-the-app", Method: "POST", Path: "/v1/secret-path",
	}})
	for _, leak := range []string{"203.0.113.9", "model-chosen-by-the-app", "secret-path"} {
		if strings.Contains(body, leak) {
			t.Errorf("metrics contain %q, which an app chooses or which identifies a caller", leak)
		}
	}
}

func TestLabelValuesAreEscaped(t *testing.T) {
	body := scrape(t, []events.Event{{Kind: events.KindRequest, KeyID: "a\"b\\c\nd", Status: 200}})
	wantLine(t, body, `kman_requests_total{key_id="a\"b\\c\nd",status_class="2xx",upstream_id=""} 1`)
}

func TestExtraMetricsAreServedWithTheCounts(t *testing.T) {
	body := scrape(t, nil, events.Metric{
		Name: "kman_price_data_age_seconds", Help: "Age of the price table.", Type: events.Gauge,
		Samples: func() []events.Sample { return []events.Sample{{Value: 86400}} },
	})
	wantLine(t, body, "kman_price_data_age_seconds 86400")
	wantLine(t, body, "# TYPE kman_price_data_age_seconds gauge")
}

func TestBusAndWriterMetricsShowDrops(t *testing.T) {
	b := events.New(events.Options{Capacity: 1, PriorityCapacity: 1})
	sub := b.Subscribe("store", events.SubOptions{Buffer: 1})
	_ = sub
	for range 4 {
		b.Emit(events.Event{KeyID: "k"})
	}
	b.Emit(events.Event{KeyID: "k", Priority: true})
	b.Emit(events.Event{KeyID: "k", Priority: true})
	w := events.NewWriter(events.WriterOptions{Sink: &sink{}})
	m := events.NewMetrics()
	rec := httptest.NewRecorder()
	m.Handler(append(events.BusMetrics(b), events.WriterMetrics(w)...)...).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	wantLine(t, body, `kman_events_dropped_total{lane="ordinary"} 3`)
	wantLine(t, body, `kman_events_dropped_total{lane="priority"} 1`)
	wantLine(t, body, "kman_event_queue_depth 2")
	wantLine(t, body, "kman_events_written_total 0")
	wantLine(t, body, "kman_event_write_failures_total 0")
	wantLine(t, body, "kman_events_lost_total 0")
	wantLine(t, body, `kman_event_subscriber_dropped_total{subscriber="store"} 0`)
}
