package store_test

import (
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

func TestEventsAreStoredWithEveryFieldAndReadBackBySince(t *testing.T) {
	s := open(t)
	ts := time.Date(2026, 11, 11, 10, 30, 15, 123_000_000, time.UTC)
	want := store.EventRow{
		Time: ts, Kind: "request", KeyID: "abcd1234", ClientIP: "203.0.113.7", Country: "DE", ASN: 3320,
		UpstreamID: "up_1", Model: "gpt-5.4", Operation: "POST /v1/chat/completions",
		Status: 200, LatencyMS: 812, ReqBytes: 1200, RespBytes: 5400,
		TokensIn: 10, TokensOut: 20, TokensCached: 3, TokensCacheWrite: 4, TokensAudioIn: 5, TokensAudioOut: 6,
		CostMicro: 1234, CostKnown: true, CostEstimated: true,
	}
	blocked := store.EventRow{Time: ts.Add(time.Second), Kind: "blocked", ClientIP: "198.51.100.1", Status: 401, BlockedCode: "invalid_key"}
	do(t, s, func(tx *store.Tx) error { return tx.AddEvents([]store.EventRow{want, blocked}) })

	var got []store.EventRow
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.EventsSince(ts)
		return err
	})
	if len(got) != 2 || got[0] != want || got[1] != blocked {
		t.Fatalf("EventsSince =\n%+v\nwant\n%+v\n%+v", got, want, blocked)
	}
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.EventsSince(ts.Add(time.Second))
		return err
	})
	if len(got) != 1 || got[0].Kind != "blocked" {
		t.Fatalf("EventsSince(later) = %+v, want only the blocked event", got)
	}
}

func TestAnUnknownCostIsStoredAsNoCostNotZero(t *testing.T) {
	s := open(t)
	ts := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: ts, Kind: "request", KeyID: "abcd1234", Status: 200, CostKnown: false},
			{Time: ts, Kind: "request", KeyID: "abcd1234", Status: 200, CostKnown: true, CostMicro: 0},
		})
	})
	var got []store.EventRow
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.EventsSince(ts)
		return err
	})
	if len(got) != 2 || got[0].CostKnown || !got[1].CostKnown {
		t.Fatalf("got %+v, want the first cost unknown and the second a known zero", got)
	}
}

func TestUsageHourlyIsIncrementedWithEachBatch(t *testing.T) {
	s := open(t)
	h10 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	h11 := h10.Add(time.Hour)
	batch1 := []store.EventRow{
		{Time: h10.Add(5 * time.Minute), Kind: "request", KeyID: "kkkk1111", Status: 200, TokensIn: 10, TokensOut: 20, CostMicro: 100, CostKnown: true},
		{Time: h10.Add(6 * time.Minute), Kind: "request", KeyID: "kkkk1111", Status: 429, CostKnown: true},
		{Time: h10.Add(7 * time.Minute), Kind: "request", KeyID: "kkkk1111", Status: 502},
		{Time: h10.Add(8 * time.Minute), Kind: "blocked", KeyID: "kkkk1111", Status: 403, BlockedCode: "key_disabled"},
		{Time: h10.Add(9 * time.Minute), Kind: "blocked", Status: 401, BlockedCode: "invalid_key"}, // no key: no usage row
		{Time: h11.Add(time.Minute), Kind: "request", KeyID: "kkkk1111", Status: 200, TokensIn: 1, CostMicro: 7, CostKnown: true},
		{Time: h10.Add(time.Minute), Kind: "request", KeyID: "kkkk2222", Status: 200, CostMicro: 5, CostKnown: true},
	}
	batch2 := []store.EventRow{
		{Time: h10.Add(30 * time.Minute), Kind: "request", KeyID: "kkkk1111", Status: 200, TokensIn: 5, TokensOut: 5, CostMicro: 50, CostKnown: true},
	}
	do(t, s, func(tx *store.Tx) error { return tx.AddEvents(batch1) })
	do(t, s, func(tx *store.Tx) error { return tx.AddEvents(batch2) })

	var got []store.UsageRow
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.UsageSince(h10)
		return err
	})
	want := []store.UsageRow{
		{KeyID: "kkkk1111", Hour: h10, Requests: 4, Blocked: 1, Errors4xx: 1, Errors5xx: 1, TokensIn: 15, TokensOut: 25, CostMicro: 150, Unmetered: 1},
		{KeyID: "kkkk1111", Hour: h11, Requests: 1, TokensIn: 1, CostMicro: 7},
		{KeyID: "kkkk2222", Hour: h10, Requests: 1, CostMicro: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("UsageSince = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v\nwant   %+v", i, got[i], want[i])
		}
	}
}

func TestABatchIsOneTransaction(t *testing.T) {
	s := open(t)
	ts := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	// A kind the schema refuses fails the whole batch, events and usage alike.
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: ts, Kind: "request", KeyID: "kkkk1111", Status: 200},
			{Time: ts, Kind: "nonsense", KeyID: "kkkk1111"},
		})
	})
	if err == nil {
		t.Fatal("AddEvents accepted an event of an unknown kind")
	}
	var ev []store.EventRow
	var use []store.UsageRow
	do(t, s, func(tx *store.Tx) (err error) {
		if ev, err = tx.EventsSince(ts); err != nil {
			return err
		}
		use, err = tx.UsageSince(ts)
		return err
	})
	if len(ev) != 0 || len(use) != 0 {
		t.Fatalf("a failed batch left %d events and %d usage rows", len(ev), len(use))
	}
}

func TestLastUsedIsTheLatestHourWithAForwardedRequest(t *testing.T) {
	s := open(t)
	d := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: d.Add(3*time.Hour + 20*time.Minute), Kind: "request", KeyID: "aaaaaaaa", Status: 200},
			{Time: d.Add(5*time.Hour + 5*time.Minute), Kind: "session", KeyID: "aaaaaaaa", Status: 200},
			{Time: d.Add(9 * time.Hour), Kind: "blocked", KeyID: "aaaaaaaa", Status: 403, BlockedCode: "address_not_allowed"}, // an attempt, not a use
			{Time: d.Add(48 * time.Hour), Kind: "request", KeyID: "bbbbbbbb", Status: 502},
			{Time: d.Add(72 * time.Hour), Kind: "blocked", KeyID: "cccccccc", Status: 403, BlockedCode: "invalid_key"},
			{Time: d.Add(80 * time.Hour), Kind: "request", Status: 200}, // no key
		})
	})
	var got map[string]time.Time
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.LastUsed()
		return err
	})
	want := map[string]time.Time{"aaaaaaaa": d.Add(5 * time.Hour), "bbbbbbbb": d.Add(48 * time.Hour)}
	if len(got) != len(want) || !got["aaaaaaaa"].Equal(want["aaaaaaaa"]) || !got["bbbbbbbb"].Equal(want["bbbbbbbb"]) {
		t.Errorf("LastUsed = %v, want %v (the start of the latest hour with a forwarded request; blocked attempts do not count)", got, want)
	}
}
