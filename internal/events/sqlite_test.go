package events_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

func TestEventsThroughTheBusLandInTheStoreWithHourlyUsage(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 30, 0, 0, time.UTC)
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b := events.New(events.Options{})
	w := events.NewWriter(events.WriterOptions{Sink: events.SQLite(st)})
	sub := b.Subscribe("store", events.SubOptions{Buffer: 100})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { b.Run(ctx) })
	wg.Go(func() { w.Run(sub) })

	b.Emit(events.Event{Time: now, Kind: events.KindRequest, KeyID: "abcd1234", UpstreamID: "up_1", Model: "gpt-5.4",
		Method: "POST", Path: "/v1/chat/completions", ClientIP: netip.MustParseAddr("203.0.113.7"), Status: 200, Latency: 250 * time.Millisecond,
		TokensIn: 11, TokensOut: 22, CostMicro: 330, CostKnown: true})
	b.Emit(events.Event{Time: now, Kind: events.KindBlocked, KeyID: "abcd1234", Status: 403, BlockedCode: "key_disabled", Priority: true})
	cancel()
	wg.Wait()

	var ev []store.EventRow
	var use []store.UsageRow
	if err := st.Do(context.Background(), func(tx *store.Tx) (err error) {
		if ev, err = tx.EventsSince(now); err != nil {
			return err
		}
		use, err = tx.UsageSince(now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(ev) != 2 {
		t.Fatalf("stored %d events, want 2", len(ev))
	}
	var req, blocked store.EventRow
	for _, e := range ev {
		if e.Kind == "request" {
			req = e
		} else {
			blocked = e
		}
	}
	if req.Model != "gpt-5.4" || req.LatencyMS != 250 || req.CostMicro != 330 || req.Operation != "POST /v1/chat/completions" || req.ClientIP != "203.0.113.7" || !req.CostKnown || blocked.BlockedCode != "key_disabled" {
		t.Errorf("stored %+v and %+v", req, blocked)
	}
	if len(use) != 1 || use[0].Requests != 1 || use[0].Blocked != 1 || use[0].TokensIn != 11 || use[0].CostMicro != 330 {
		t.Errorf("usage %+v, want one request, one blocked, 11 tokens in, 330 microdollars", use)
	}
}
