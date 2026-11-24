package store

import (
	"context"
	"errors"
	"strings"
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
		t.Errorf("baseline_places holds %d rows (%v), want only the key's 3 and no client", n, err)
	}
	// The events from before have no client, which is not the same as none: no
	// header was not recorded then, and no place is made up for it.
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE client IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d migrated events have a client (%v), want none", n, err)
	}
}

func TestMigration0002AddsTheClientColumnAndTheClientKind(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	var typ string
	if err := s.db.QueryRowContext(ctx, `SELECT type FROM pragma_table_info('events') WHERE name = 'client'`).Scan(&typ); err != nil || typ != "TEXT" {
		t.Fatalf("events.client = %q (%v), want a TEXT column", typ, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO baseline_places (key_id, kind, value, first_seen, last_seen) VALUES ('kkkk1111', 'client', 'curl/8', 1, 1)`); err != nil {
		t.Errorf("baseline_places refuses the client kind: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO baseline_places (key_id, kind, value, first_seen, last_seen) VALUES ('kkkk1111', 'user_agent', 'curl/8', 1, 1)`); err == nil {
		t.Error("baseline_places accepts a kind it does not know")
	}
}

func TestMigration0002AddsTheCanariesTable(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('canaries') WHERE name IN
		('key_id', 'planted', 'ignore_json', 'armed_at', 'fired_at', 'last_fired_at', 'fires', 'last_ip', 'last_country', 'last_asn', 'last_as_org', 'last_client')`).Scan(&n); err != nil || n != 12 {
		t.Fatalf("canaries has %d of its 12 columns (%v)", n, err)
	}
	// A canary row must belong to a key.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO canaries (key_id, planted, armed_at) VALUES ('nokey000', 'x', 1)`); err == nil {
		t.Error("canaries accepts a row for a key that does not exist")
	}
}

// Until v0.2.0 is tagged, migration 0002 grows with each M2 ticket, so a data
// directory made by an earlier build of it is at schema 2 and lacks what a
// later build reads. Opening it must say so, not fail on the first query that
// touches the missing table.
func TestADatabaseFromAnEarlierBuildOfMigration0002IsRefusedWithTheFix(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := openTest(t, dir)
	if _, err := s.db.ExecContext(ctx, `DROP TABLE canaries`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ctx, Options{Dir: dir, Now: testNow})
	if err == nil {
		t.Fatal("Open accepted a schema-2 database with no canaries table")
	}
	if !errors.Is(err, ErrStaleSchema) || !strings.Contains(err.Error(), "rebuild the data directory (unreleased migration changed)") ||
		!strings.Contains(err.Error(), "canaries") {
		t.Errorf("error %q: want it to say to rebuild the data directory and name what is missing", err)
	}
}

func TestADatabaseMissingAnEarlierColumnOfMigration0002IsRefusedToo(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := openTest(t, dir)
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE events DROP COLUMN client`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, Options{Dir: dir, Now: testNow}); !errors.Is(err, ErrStaleSchema) {
		t.Errorf("Open = %v, want ErrStaleSchema for a database with no events.client", err)
	}
}
