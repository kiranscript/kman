package store_test

import (
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestAWarningIsMarkedSentOnceForAKeyPeriodAndThreshold(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateKey(store.VirtualKey{ID: "abcd1234", CreatedAt: now, PolicyJSON: "{}", TagsJSON: "[]"}, []byte("h"))
		return err
	})
	mark := func(period string, threshold int) (first bool) {
		do(t, s, func(tx *store.Tx) error {
			var err error
			first, err = tx.MarkBudgetAlert("abcd1234", period, threshold, now)
			return err
		})
		return first
	}
	if !mark("2026-11-04", 80) {
		t.Error("the first mark was not first")
	}
	if mark("2026-11-04", 80) {
		t.Error("the same warning was first twice")
	}
	if !mark("2026-11-04", 100) || !mark("2026-11-05", 80) {
		t.Error("another threshold, or another period, is another warning")
	}
}

func TestAWarningForAKeyThatDoesNotExistIsRefused(t *testing.T) {
	s := open(t)
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.MarkBudgetAlert("nokey000", "2026-11", 50, now)
		return err
	})
	if err == nil {
		t.Fatal("a warning was recorded for a key that is not there")
	}
}
