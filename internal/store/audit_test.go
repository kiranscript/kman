package store_test

import (
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestAuditEntriesReadBackInOrder(t *testing.T) {
	s := open(t)
	want := []store.AuditEntry{
		{TS: now, Actor: "owner", Action: "upstream.create", TargetKind: "upstream", TargetID: "up_1",
			AfterJSON: `{"name":"openai"}`, ClientIP: "127.0.0.1", RequestID: "req_1"},
		{TS: now.Add(1), Actor: "system:detect", Action: "key.disable", TargetKind: "key", TargetID: "Ab3dE5gH",
			IncidentID: 42, BeforeJSON: `{"status":"active"}`, AfterJSON: `{"status":"disabled"}`},
	}
	do(t, s, func(tx *store.Tx) error {
		for i := range want {
			id, err := tx.AppendAudit(want[i])
			if err != nil {
				return err
			}
			want[i].ID = id
		}
		return nil
	})
	var got []store.AuditEntry
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.AuditEntries(0, 10)
		return err
	})
	// Timestamps are stored in milliseconds.
	want[1].TS = now
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v\nwant      %+v", i, got[i], want[i])
		}
	}
	var after []store.AuditEntry
	do(t, s, func(tx *store.Tx) error {
		var err error
		after, err = tx.AuditEntries(want[0].ID, 10)
		return err
	})
	if len(after) != 1 || after[0].ID != want[1].ID {
		t.Errorf("AuditEntries after the first = %+v, want only the second", after)
	}
}

func TestLatestAuditEntriesAreTheNewestOldestFirst(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		for _, action := range []string{"a", "b", "c"} {
			if _, err := tx.AppendAudit(store.AuditEntry{TS: now, Actor: "owner", Action: action, TargetKind: "x", TargetID: "y"}); err != nil {
				return err
			}
		}
		return nil
	})
	var got []store.AuditEntry
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.LatestAuditEntries(2)
		return err
	})
	if len(got) != 2 || got[0].Action != "b" || got[1].Action != "c" {
		t.Errorf("LatestAuditEntries(2) = %+v, want b then c", got)
	}
}
