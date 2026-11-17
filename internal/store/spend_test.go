package store_test

import (
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

func TestSpendIsAddedAsIncrementsAndReadBackBySince(t *testing.T) {
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		for _, id := range []string{"abcd1234", "efgh5678"} {
			if _, err := tx.CreateKey(virtualKey(id, ""), hash); err != nil {
				return err
			}
		}
		return nil
	})
	add := func(rows ...store.SpendRow) {
		do(t, s, func(tx *store.Tx) error { return tx.AddSpend(rows) })
	}
	add(store.SpendRow{KeyID: "abcd1234", Day: "2026-11-10", CostMicro: 5, EstimatedMicro: 1, Requests: 2, Unmetered: 1})
	add(
		store.SpendRow{KeyID: "abcd1234", Day: "2026-11-10", CostMicro: 7, Requests: 1},
		store.SpendRow{KeyID: "abcd1234", Day: "2026-11-11", CostMicro: 100, Requests: 1},
		store.SpendRow{KeyID: "efgh5678", Day: "2026-10-31", CostMicro: 9, Requests: 1},
	)
	var got []store.SpendRow
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.SpendSince("2026-11-01")
		return err
	})
	want := []store.SpendRow{
		{KeyID: "abcd1234", Day: "2026-11-10", CostMicro: 12, EstimatedMicro: 1, Requests: 3, Unmetered: 1},
		{KeyID: "abcd1234", Day: "2026-11-11", CostMicro: 100, Requests: 1},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("SpendSince = %+v\nwant         %+v", got, want)
	}
}

// Spend for a key the store doesn't hold is dropped, not an error: one stray
// row must not stop every other key's spend from being kept.
func TestSpendForAKeyTheStoreDoesNotHoldIsDroppedAndTheRestIsKept(t *testing.T) {
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateKey(virtualKey("abcd1234", ""), hash)
		return err
	})
	do(t, s, func(tx *store.Tx) error {
		return tx.AddSpend([]store.SpendRow{
			{KeyID: "nokey000", Day: "2026-11-11", CostMicro: 1},
			{KeyID: "abcd1234", Day: "2026-11-11", CostMicro: 2},
		})
	})
	var got []store.SpendRow
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.SpendSince("2026-11-01")
		return err
	})
	if len(got) != 1 || got[0].KeyID != "abcd1234" || got[0].CostMicro != 2 {
		t.Errorf("SpendSince = %+v", got)
	}
}

func TestAddingNoSpendStillTouchesTheStore(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error { return tx.AddSpend(nil) })
}

func TestSpendLabelsFollowTheSpendTimezone(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*3600)
	at := time.Date(2026, 11, 30, 20, 0, 0, 0, time.UTC) // 05:00 on 1 December in Tokyo
	if day, month := store.SpendLabels(at, time.UTC); day != "2026-11-30" || month != "2026-11" {
		t.Errorf("UTC labels = %s, %s", day, month)
	}
	if day, month := store.SpendLabels(at, tokyo); day != "2026-12-01" || month != "2026-12" {
		t.Errorf("Tokyo labels = %s, %s, want the next day and month", day, month)
	}
}
