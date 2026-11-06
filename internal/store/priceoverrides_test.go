package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

func TestPutPriceOverrideInsertsThenReplaces(t *testing.T) {
	s := open(t)
	row := store.PriceOverride{Scope: store.InstallScope, Model: "gpt-x", Input: 1, Output: 2, MaxOutput: 3, ExtraJSON: `{"cached_micro":1}`, Actor: "owner", UpdatedAt: now}
	var first store.PriceOverride
	do(t, s, func(tx *store.Tx) error {
		var err error
		first, err = tx.PutPriceOverride(row)
		return err
	})
	if first.Generation == 0 {
		t.Error("no generation")
	}
	later := now.Add(time.Hour)
	row.Input, row.Actor, row.UpdatedAt = 10, "ci", later
	var second store.PriceOverride
	do(t, s, func(tx *store.Tx) error {
		var err error
		second, err = tx.PutPriceOverride(row)
		return err
	})
	if second.Generation <= first.Generation {
		t.Errorf("generations %d then %d, want rising", first.Generation, second.Generation)
	}
	row.Generation = second.Generation
	var got []store.PriceOverride
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.PriceOverrides()
		return err
	})
	if len(got) != 1 || got[0] != row {
		t.Errorf("PriceOverrides = %+v, want [%+v]", got, row)
	}
}

func TestPriceOverridesAreOnePerScopeAndModelInOrder(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		for _, k := range [][2]string{{"up_b", "m"}, {store.InstallScope, "z"}, {"up_b", "a"}, {store.InstallScope, "m"}} {
			if _, err := tx.PutPriceOverride(store.PriceOverride{Scope: k[0], Model: k[1], ExtraJSON: "{}", Actor: "owner", UpdatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.PriceOverrides()
		if err != nil {
			return err
		}
		var keys []string
		for _, o := range got {
			keys = append(keys, o.Scope+"/"+o.Model)
		}
		if len(keys) != 4 || keys[0] != "install/m" || keys[1] != "install/z" || keys[2] != "up_b/a" || keys[3] != "up_b/m" {
			t.Errorf("order = %v", keys)
		}
		return nil
	})
}

func TestPriceOverrideFindsAndDeletesOneRow(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.PutPriceOverride(store.PriceOverride{Scope: "up_a", Model: "m", Input: 5, ExtraJSON: "{}", Actor: "owner", UpdatedAt: now})
		return err
	})
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.PriceOverride("up_a", "m")
		if err != nil || got.Input != 5 {
			t.Errorf("PriceOverride = %+v, %v", got, err)
		}
		if _, err := tx.PriceOverride(store.InstallScope, "m"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("another scope's override: %v, want not found", err)
		}
		if err := tx.DeletePriceOverride("up_a", "other"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("deleting what isn't there: %v, want not found", err)
		}
		return tx.DeletePriceOverride("up_a", "m")
	})
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.PriceOverride("up_a", "m"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("after delete: %v, want not found", err)
		}
		return nil
	})
}

func TestPriceOverridesRollBackWithTheTransaction(t *testing.T) {
	s := open(t)
	boom := errors.New("boom")
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		if _, err := tx.PutPriceOverride(store.PriceOverride{Scope: store.InstallScope, Model: "m", ExtraJSON: "{}", Actor: "owner", UpdatedAt: now}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.PriceOverrides()
		if len(got) != 0 {
			t.Errorf("rolled back, but found %+v", got)
		}
		return err
	})
}
