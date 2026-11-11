package meter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/store"
)

// openStore is a store on a fresh database, with one key per id given.
func openStore(t *testing.T, dir string, keys ...string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), store.Options{Dir: dir, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if len(keys) == 0 {
		return s
	}
	err = s.Do(context.Background(), func(tx *store.Tx) error {
		if err := tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: t0}); err != nil {
			return err
		}
		u, err := tx.CreateUpstream(store.Upstream{Name: "u", Profile: "openai", Host: "h", Port: 443, TLS: true,
			PathAllowlistJSON: `["* /**"]`, PriceMultiplierPPM: 1_000_000, CreatedAt: t0, UpdatedAt: t0})
		if err != nil {
			return err
		}
		if _, err := tx.CreateCredential(store.Credential{ID: "cr_1", UpstreamID: u.ID, Name: "c", CreatedAt: t0, Ciphertext: []byte{1}, Nonce: []byte{1}, DataKeyID: "dek_1"}); err != nil {
			return err
		}
		for _, id := range keys {
			if _, err := tx.CreateKey(store.VirtualKey{ID: id, CredentialID: "cr_1", PolicyJSON: `{}`, PolicyVersion: 1, TagsJSON: `[]`,
				ExpiresAt: t0.Add(time.Hour), CreatedAt: t0}, make([]byte, 32)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSpendSurvivesARestartThroughTheSQLiteCounters(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, "k")
	m, _ := newMeter(t, func(o *meter.Options) { o.Counters = meter.SQLite(s) })
	spendOnce(t, m, "k", t0)
	spendOnce(t, m, "k", t0)
	if err := m.Flush(context.Background(), t0); err != nil {
		t.Fatal(err)
	}

	again, _ := newMeter(t, func(o *meter.Options) { o.Counters = meter.SQLite(s) })
	if err := again.Load(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	if got := again.Spend("k", t0); got.Day.CostMicro != 140 || got.Month.Requests != 2 {
		t.Errorf("after a restart: %+v", got)
	}
}

// The failure test for a locked store: another writer holds the database, so
// flushes fail. Spend isn't lost, a capped key fails closed after a minute, and
// the key is let back in when the store is free again.
func TestAStoreThatIsLockedFailsCappedKeysClosedAndLosesNothing(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, "k")
	other, err := store.Open(context.Background(), store.Options{Dir: dir, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	m, _ := newMeter(t, func(o *meter.Options) { o.Counters = meter.SQLite(s) })
	spendOnce(t, m, "k", t0)

	held, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- other.Do(context.Background(), func(tx *store.Tx) error {
			close(held) // the write lock is taken as the transaction begins
			<-release
			return nil
		})
	}()
	<-held

	short := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 300*time.Millisecond)
	}
	ctx, cancel := short()
	if err := m.Flush(ctx, t0); err == nil {
		t.Fatal("a flush into a locked database reported success")
	}
	cancel()
	// The deadline is the first failure plus a minute.
	if _, err := m.Reserve(t0.Add(30*time.Second), capped("k", 1_000_000, 0)); err != nil {
		t.Errorf("a capped key was refused after 30 s: %v", err)
	}
	if _, err := m.Reserve(t0.Add(61*time.Second), capped("k", 1_000_000, 0)); !errors.Is(err, meter.ErrStoreUnavailable) {
		t.Errorf("a capped key after a minute of a locked store: %v, want ErrStoreUnavailable", err)
	}
	if _, err := m.Reserve(t0.Add(61*time.Second), ask("k")); err != nil {
		t.Errorf("a key with no cap was refused: %v", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := m.Flush(context.Background(), t0.Add(70*time.Second)); err != nil {
		t.Fatalf("a flush once the lock was let go: %v", err)
	}
	if _, err := m.Reserve(t0.Add(71*time.Second), capped("k", 1_000_000, 0)); err != nil {
		t.Errorf("after the store came back: %v", err)
	}
	rows, err := meter.SQLite(s).Since(context.Background(), "2026-11-01")
	if err != nil || len(rows) != 1 || rows[0].CostMicro != 70 {
		t.Errorf("the store holds %+v, %v: the spend made while it was locked is lost", rows, err)
	}
}
