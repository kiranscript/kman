package store

import (
	"context"
	"testing"
	"time"
)

func TestMigration0002FillsPlacesFromTheEventsAlreadyStored(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

	// A database at schema 1, with events, as v0.1.0 left it.
	old, err := open(ctx, Options{Dir: dir, Now: testNow}, migrations[:1])
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO events (ts, key_id, kind, client_ip, country, asn, status) VALUES (?, 'kkkk1111', 'request', '203.0.113.7', 'DE', 3320, 200)`,
		`INSERT INTO events (ts, key_id, kind, client_ip, country, asn, status) VALUES (?, 'kkkk1111', 'blocked', '203.0.113.8', 'DE', 3320, 403)`,
		`INSERT INTO events (ts, key_id, kind, client_ip, status) VALUES (?, NULL, 'blocked', '198.51.100.1', 401)`,
	} {
		if _, err := old.db.ExecContext(ctx, q, toMilli(t0)); err != nil {
			t.Fatal(err)
		}
		t0 = t0.Add(time.Minute)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	s := openTest(t, dir)
	if got := userVersion(t, s); got != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", got, SchemaVersion)
	}
	var got []PlaceRow
	if err := s.Do(ctx, func(tx *Tx) (err error) { got, err = tx.Places("kkkk1111", ""); return }); err != nil {
		t.Fatal(err)
	}
	first, last := t0.Add(-3*time.Minute), t0.Add(-2*time.Minute)
	want := []PlaceRow{
		{KeyID: "kkkk1111", Kind: PlaceASN, Value: "3320", FirstSeen: first, LastSeen: last, Count: 1, Blocked: 1},
		{KeyID: "kkkk1111", Kind: PlaceCountry, Value: "DE", FirstSeen: first, LastSeen: last, Count: 1, Blocked: 1},
		{KeyID: "kkkk1111", Kind: PlaceNetwork, Value: "203.0.113.0/24", FirstSeen: first, LastSeen: last, Count: 1, Blocked: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("places = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("place %d = %+v\nwant      %+v", i, got[i], want[i])
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM baseline_places`).Scan(&n); err != nil || n != 3 {
		t.Errorf("baseline_places holds %d rows (%v), want only the key's 3", n, err)
	}
}
