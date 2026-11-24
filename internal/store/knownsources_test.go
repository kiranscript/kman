package store_test

import (
	"errors"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestAKnownSourceReadsBackAndListsInstallWideBeforePerKey(t *testing.T) {
	s := withKeys(t)
	do(t, s, func(tx *store.Tx) error {
		perKey, err := tx.CreateKnownSource(store.KnownSource{Scope: "abcd1234", Kind: store.SourceCountry, Value: "DE", Label: "the Berlin office", CreatedBy: "owner", CreatedAt: now})
		if err != nil {
			return err
		}
		wide, err := tx.CreateKnownSource(store.KnownSource{Kind: store.SourceCIDR, Value: "203.0.113.0/24", Label: "CI runners", CreatedBy: "owner", CreatedAt: now})
		if err != nil {
			return err
		}
		if perKey.ID == 0 || wide.ID == perKey.ID || perKey.Generation == 0 || wide.Scope != "" || !perKey.CreatedAt.Equal(now) {
			t.Errorf("CreateKnownSource = %+v, %+v", perKey, wide)
		}
		got, err := tx.KnownSource(perKey.ID)
		if err != nil || got != perKey {
			t.Errorf("KnownSource = %+v (%v), want %+v", got, err, perKey)
		}
		all, err := tx.KnownSources()
		if err != nil || len(all) != 2 || all[0].ID != wide.ID || all[1].ID != perKey.ID {
			t.Errorf("KnownSources = %+v (%v), want the install-wide one first", all, err)
		}
		forKey, err := tx.KnownSourcesFor("abcd1234")
		if err != nil || len(forKey) != 2 {
			t.Errorf("KnownSourcesFor(abcd1234) = %+v (%v), want both", forKey, err)
		}
		forOther, err := tx.KnownSourcesFor("beef5678")
		if err != nil || len(forOther) != 1 || forOther[0].ID != wide.ID {
			t.Errorf("KnownSourcesFor(beef5678) = %+v (%v), want only the install-wide one", forOther, err)
		}
		if n, err := tx.CountKnownSources("abcd1234"); err != nil || n != 1 {
			t.Errorf("CountKnownSources = %d (%v)", n, err)
		}
		return nil
	})
}

func TestAKnownSourceIsNotAddedTwiceInOneScope(t *testing.T) {
	s := withKeys(t)
	do(t, s, func(tx *store.Tx) error {
		ks := store.KnownSource{Kind: store.SourceASN, Value: "3320", Label: "Telekom", CreatedBy: "owner", CreatedAt: now}
		if _, err := tx.CreateKnownSource(ks); err != nil {
			return err
		}
		if _, err := tx.CreateKnownSource(ks); !errors.Is(err, store.ErrConflict) {
			t.Errorf("the same known source again = %v, want ErrConflict", err)
		}
		ks.Scope = "abcd1234" // the same value on one key is a different source
		if _, err := tx.CreateKnownSource(ks); err != nil {
			t.Errorf("the same value for one key: %v", err)
		}
		return nil
	})
}

func TestAKnownSourceIsDeleted(t *testing.T) {
	s := withKeys(t)
	do(t, s, func(tx *store.Tx) error {
		ks, err := tx.CreateKnownSource(store.KnownSource{Kind: store.SourceClient, Value: "curl/8", Label: "ci", CreatedBy: "owner", CreatedAt: now})
		if err != nil {
			return err
		}
		if err := tx.DeleteKnownSource(ks.ID); err != nil {
			t.Errorf("DeleteKnownSource: %v", err)
		}
		if _, err := tx.KnownSource(ks.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("KnownSource after delete = %v, want ErrNotFound", err)
		}
		if err := tx.DeleteKnownSource(ks.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("a second delete = %v, want ErrNotFound", err)
		}
		return nil
	})
}
