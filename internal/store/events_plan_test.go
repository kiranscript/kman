package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A busy key's newest page must come from events_by_key in id order. With only
// events_key_ts SQLite reads every event of the key and sorts them, which holds
// the store's one connection for as long as that takes (#109).
func TestListEventsForAKeyReadsTheIndexInIDOrderWithoutSorting(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	for name, f := range map[string]EventFilter{
		"a key":              {KeyID: "abcd1234"},
		"a key, next page":   {KeyID: "abcd1234", BeforeID: 500},
		"a key and its kind": {KeyID: "abcd1234", Kinds: []string{"blocked"}},
		"a key since":        {KeyID: "abcd1234", Since: testNow.Add(-7 * 24 * time.Hour)},
		"a key in a window":  {KeyID: "abcd1234", Since: testNow.Add(-7 * 24 * time.Hour), Until: testNow},
	} {
		query, args := listEventsQuery(f, 51)
		rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, "; ")
		if !strings.Contains(joined, "events_by_key") || strings.Contains(joined, "TEMP B-TREE") {
			t.Errorf("%s: plan %q, want events_by_key and no sort", name, joined)
		}
	}
}

func TestADatabaseWithoutTheEventsByKeyIndexIsRefusedWithTheFix(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := openTest(t, dir)
	if _, err := s.db.ExecContext(ctx, `DROP INDEX events_by_key`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ctx, Options{Dir: dir, Now: testNow})
	if err == nil || !strings.Contains(err.Error(), "events_by_key") {
		t.Errorf("Open without events_by_key = %v, want ErrStaleSchema naming it", err)
	}
}
