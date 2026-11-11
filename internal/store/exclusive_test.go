package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestOpenExclusiveIsRefusedWhileAnotherStoreHoldsTheDatabase(t *testing.T) {
	dir := t.TempDir()
	held, err := store.Open(context.Background(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	// An idle connection holds the database too: that is a server between requests.
	do(t, held, func(tx *store.Tx) error { _, err := tx.Meta(); return err })

	s, err := store.OpenExclusive(context.Background(), store.Options{Dir: dir, Now: now})
	if err == nil {
		s.Close()
		t.Fatal("OpenExclusive succeeded while another store held the database")
	}
	if !errors.Is(err, store.ErrHeld) {
		t.Errorf("OpenExclusive: %v, want ErrHeld", err)
	}
	// The refusal cost the holder nothing.
	do(t, held, func(tx *store.Tx) error { _, err := tx.Generation(); return err })
}

func TestOpenExclusiveWorksOnceTheHolderHasClosed(t *testing.T) {
	dir := t.TempDir()
	held, err := store.Open(context.Background(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenExclusive(context.Background(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatalf("OpenExclusive: %v", err)
	}
	defer s.Close()
	do(t, s, func(tx *store.Tx) error { _, err := tx.Generation(); return err })
}

func TestAStoreCannotOpenWhileOneIsExclusive(t *testing.T) {
	dir := t.TempDir()
	first, err := store.Open(context.Background(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	s, err := store.OpenExclusive(context.Background(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatalf("OpenExclusive: %v", err)
	}
	defer s.Close()
	second, err := store.Open(context.Background(), store.Options{Dir: dir, Now: now})
	if err == nil {
		second.Close()
		t.Fatal("Open succeeded while a store held the database exclusively")
	}
	if !errors.Is(err, store.ErrHeld) {
		t.Errorf("Open: %v, want ErrHeld", err)
	}
}

func TestOpenExclusiveLeavesNoSideFilesOnceClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := store.OpenExclusive(context.Background(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatalf("OpenExclusive: %v", err)
	}
	do(t, s, func(tx *store.Tx) error { _, err := tx.Generation(); return err })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"-wal", "-shm"} {
		if m, _ := filepath.Glob(filepath.Join(dir, store.FileName+side)); len(m) != 0 {
			t.Errorf("%s is still there after Close", m[0])
		}
	}
	// What it wrote is there for the next open, under the usual locking.
	again, err := store.Open(context.Background(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatalf("Open afterwards: %v", err)
	}
	defer again.Close()
	do(t, again, func(tx *store.Tx) error {
		m, err := tx.Meta()
		if err == nil && m.Generation != 1 {
			t.Errorf("generation = %d, want 1", m.Generation)
		}
		return err
	})
}
