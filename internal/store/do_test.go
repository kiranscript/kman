package store_test

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// open opens a store in its own temporary directory, so each test gets its
// own database.
func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// do runs fn in one transaction and fails the test if it errs.
func do(t *testing.T, s *store.Store, fn func(*store.Tx) error) {
	t.Helper()
	if err := s.Do(context.Background(), fn); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func openai(name string) store.Upstream {
	return store.Upstream{
		Name: name, Preset: "openai", PresetVersion: 1, Profile: "openai",
		Host: "api.openai.com", Port: 443, TLS: true,
		PathAllowlistJSON: `["/v1/"]`, PriceMultiplierPPM: 1_000_000,
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestDoCommits(t *testing.T) {
	s := open(t)
	var created store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		created, err = tx.CreateUpstream(openai("openai"))
		return err
	})
	var got store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.UpstreamByName("openai")
		return err
	})
	if got != created {
		t.Errorf("read back %+v\nwant        %+v", got, created)
	}
	if got.Host != "api.openai.com" || !got.TLS || got.CreatedAt != now {
		t.Errorf("read back %+v", got)
	}
}

func TestDoRollsBackOnError(t *testing.T) {
	s := open(t)
	boom := errors.New("boom")
	err := s.Do(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.CreateUpstream(openai("openai")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Do returned %v, want %v", err, boom)
	}
	assertNoUpstream(t, s, "openai")
}

func TestDoRollsBackOnPanic(t *testing.T) {
	s := open(t)
	func() {
		defer func() {
			if p := recover(); p != "boom" {
				t.Errorf("recovered %v, want the panic re-raised", p)
			}
		}()
		s.Do(context.Background(), func(tx *store.Tx) error {
			if _, err := tx.CreateUpstream(openai("openai")); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	assertNoUpstream(t, s, "openai")
	// The connection is free again.
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateUpstream(openai("openai"))
		return err
	})
}

func assertNoUpstream(t *testing.T, s *store.Store, name string) {
	t.Helper()
	err := s.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.UpstreamByName(name)
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UpstreamByName(%q) = %v, want ErrNotFound", name, err)
	}
}

func TestEachTransactionStampsOneGeneration(t *testing.T) {
	s := open(t)
	var a, b, c store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		if a, err = tx.CreateUpstream(openai("a")); err != nil {
			return err
		}
		b, err = tx.CreateUpstream(openai("b"))
		return err
	})
	// A transaction that changes nothing takes no generation, and one that
	// rolls back gives its generation back.
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.UpstreamByName("a")
		return err
	})
	s.Do(context.Background(), func(tx *store.Tx) error {
		tx.CreateUpstream(openai("rolled-back"))
		return errors.New("roll back")
	})
	var m store.Meta
	do(t, s, func(tx *store.Tx) error {
		var err error
		if c, err = tx.CreateUpstream(openai("c")); err != nil {
			return err
		}
		m, err = tx.Meta()
		return err
	})
	if a.Generation != 1 || b.Generation != 1 {
		t.Errorf("first transaction stamped %d and %d, want 1 and 1", a.Generation, b.Generation)
	}
	if c.Generation != 2 {
		t.Errorf("next writing transaction stamped %d, want 2", c.Generation)
	}
	if m.Generation != 2 {
		t.Errorf("meta generation = %d, want 2", m.Generation)
	}
}

func TestDuplicateLiveNameIsErrConflict(t *testing.T) {
	s := open(t)
	var first store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		first, err = tx.CreateUpstream(openai("openai"))
		return err
	})
	err := s.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.CreateUpstream(openai("openai"))
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second CreateUpstream = %v, want ErrConflict", err)
	}
	// Deleting the first frees the name.
	do(t, s, func(tx *store.Tx) error {
		if err := tx.DeleteUpstream(first.ID, now); err != nil {
			return err
		}
		_, err := tx.CreateUpstream(openai("openai"))
		return err
	})
}

func TestMissingRowsAreErrNotFound(t *testing.T) {
	s := open(t)
	var u store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		if u, err = tx.CreateUpstream(openai("openai")); err != nil {
			return err
		}
		return tx.DeleteUpstream(u.ID, now)
	})
	for name, fn := range map[string]func(*store.Tx) error{
		"Upstream of a missing id":    func(tx *store.Tx) error { _, err := tx.Upstream("up_000000000000"); return err },
		"UpstreamByName of a missing": func(tx *store.Tx) error { _, err := tx.UpstreamByName("nope"); return err },
		"Upstream of a deleted id":    func(tx *store.Tx) error { _, err := tx.Upstream(u.ID); return err },
		"DeleteUpstream of a missing": func(tx *store.Tx) error { return tx.DeleteUpstream("up_000000000000", now) },
		"DeleteUpstream of a deleted": func(tx *store.Tx) error { return tx.DeleteUpstream(u.ID, now) },
	} {
		if err := s.Do(context.Background(), fn); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", name, err)
		}
	}
}

func TestUpstreamIDsArePrefixedAndRandom(t *testing.T) {
	s := open(t)
	var a, b store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		if a, err = tx.CreateUpstream(openai("a")); err != nil {
			return err
		}
		b, err = tx.CreateUpstream(openai("b"))
		return err
	})
	re := regexp.MustCompile(`^up_[0-9A-Za-z]{12}$`)
	for _, id := range []string{a.ID, b.ID} {
		if !re.MatchString(id) {
			t.Errorf("id %q does not match %s", id, re)
		}
	}
	if a.ID == b.ID {
		t.Errorf("two upstreams share id %q", a.ID)
	}
}
