package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// oldBlob and newBlob are distinctive, so a test can look for them in the
// database's bytes.
func oldBlob(what string) []byte { return []byte("OLD-SEALED-BYTES-" + what + "-0123456789") }
func newBlob(what string) []byte { return []byte("NEW-SEALED-BYTES-" + what + "-0123456789") }

const (
	oldDEK = "dek_oldoldoldold"
	newDEK = "dek_newnewnewnew"
)

// rekeyable seeds a data key with a live and a deleted row of each table that
// holds sealed secrets. Alert channels are raw SQL until #45 gives them a repo.
func rekeyable(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	err := s.Do(ctx, func(tx *Tx) error {
		if err := tx.CreateDataKey(DataKey{ID: oldDEK, Wrapped: oldBlob("wrapped"), Nonce: oldBlob("wrap-nonce"), KEKFingerprint: "mkf_old", CreatedAt: testNow}); err != nil {
			return err
		}
		u, err := tx.CreateUpstream(Upstream{Name: "up", Profile: "generic", Host: "h", Port: 443, CreatedAt: testNow, UpdatedAt: testNow})
		if err != nil {
			return err
		}
		for _, id := range []string{"cr_live", "cr_gone"} {
			if _, err := tx.CreateCredential(Credential{ID: id, UpstreamID: u.ID, Name: id, Ciphertext: oldBlob(id), Nonce: oldBlob(id + "-nonce"), DataKeyID: oldDEK, CreatedAt: testNow}); err != nil {
				return err
			}
		}
		return tx.DeleteCredential("cr_gone", testNow)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id      string
		deleted any
	}{{"ch_live", nil}, {"ch_gone", testNow.UnixMilli()}} {
		_, err := s.db.ExecContext(ctx, `INSERT INTO alert_channels (id, name, kind, config_ciphertext, nonce, dek_id, display_host,
			min_severity, created_at, updated_at, deleted_at, generation) VALUES (?, ?, 'slack', ?, ?, ?, 'h', 'info', 1, 1, ?, 1)`,
			c.id, c.id, oldBlob(c.id), oldBlob(c.id+"-nonce"), oldDEK, c.deleted)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func replacement() (DataKey, []SealedSecret) {
	return DataKey{ID: newDEK, Wrapped: newBlob("wrapped"), Nonce: newBlob("wrap-nonce"), KEKFingerprint: "mkf_new", CreatedAt: testNow},
		[]SealedSecret{
			{Kind: SecretCredential, ID: "cr_live", Ciphertext: newBlob("cr_live"), Nonce: newBlob("cr_live-nonce"), DataKeyID: newDEK},
			{Kind: SecretAlertChannel, ID: "ch_live", Ciphertext: newBlob("ch_live"), Nonce: newBlob("ch_live-nonce"), DataKeyID: newDEK},
		}
}

func TestSealedSecretsAreTheLiveRowsOfEveryTableThatHoldsThem(t *testing.T) {
	s := openTest(t, t.TempDir())
	rekeyable(t, s)
	var got []SealedSecret
	err := s.Do(context.Background(), func(tx *Tx) error {
		var err error
		got, err = tx.SealedSecrets()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]SecretKind{"cr_live": SecretCredential, "ch_live": SecretAlertChannel}
	if len(got) != len(want) {
		t.Fatalf("SealedSecrets = %+v, want %v", got, want)
	}
	for _, sec := range got {
		if want[sec.ID] != sec.Kind || !bytes.Equal(sec.Ciphertext, oldBlob(sec.ID)) || !bytes.Equal(sec.Nonce, oldBlob(sec.ID+"-nonce")) || sec.DataKeyID != oldDEK {
			t.Errorf("SealedSecrets has %+v", sec)
		}
	}
}

func TestReplaceDataKeyLeavesOnlyTheNewKeyWithEveryRowOnIt(t *testing.T) {
	s := openTest(t, t.TempDir())
	rekeyable(t, s)
	k, secrets := replacement()
	ctx := context.Background()
	if err := s.Do(ctx, func(tx *Tx) error { return tx.ReplaceDataKey(oldDEK, k, secrets, testNow) }); err != nil {
		t.Fatalf("ReplaceDataKey: %v", err)
	}
	var keys int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM data_keys`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if keys != 1 {
		t.Errorf("data_keys has %d rows, want 1", keys)
	}
	err := s.Do(ctx, func(tx *Tx) error {
		cur, err := tx.CurrentDataKey()
		if err != nil {
			return err
		}
		if cur.ID != newDEK || !bytes.Equal(cur.Wrapped, k.Wrapped) || cur.KEKFingerprint != "mkf_new" {
			t.Errorf("CurrentDataKey = %+v", cur)
		}
		got, err := tx.SealedSecrets()
		if err != nil {
			return err
		}
		for _, sec := range got {
			if sec.DataKeyID != newDEK || !bytes.Equal(sec.Ciphertext, newBlob(sec.ID)) || !bytes.Equal(sec.Nonce, newBlob(sec.ID+"-nonce")) {
				t.Errorf("after the rekey, %+v", sec)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A deleted row keeps no ciphertext: its secret has nothing left to serve.
	for _, q := range []string{
		`SELECT ciphertext, nonce, dek_id FROM credentials WHERE id = 'cr_gone'`,
		`SELECT config_ciphertext, nonce, dek_id FROM alert_channels WHERE id = 'ch_gone'`,
	} {
		var c, n []byte
		var dek string
		if err := s.db.QueryRowContext(ctx, q).Scan(&c, &n, &dek); err != nil {
			t.Fatal(err)
		}
		if len(c) != 0 || len(n) != 0 || dek != newDEK {
			t.Errorf("%s: ciphertext %q, nonce %q, data key %q", q, c, n, dek)
		}
	}
}

func TestReplaceDataKeyStampsItsGeneration(t *testing.T) {
	s := openTest(t, t.TempDir())
	rekeyable(t, s)
	k, secrets := replacement()
	ctx := context.Background()
	var gen int64
	err := s.Do(ctx, func(tx *Tx) error {
		if err := tx.ReplaceDataKey(oldDEK, k, secrets, testNow); err != nil {
			return err
		}
		var err error
		gen, err = tx.Generation()
		if err != nil {
			return err
		}
		c, err := tx.Credential("cr_live")
		if err == nil && c.Generation != gen {
			t.Errorf("credential generation = %d, want %d", c.Generation, gen)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReplaceDataKeyFailsWithALiveSecretLeftOnTheOldKey(t *testing.T) {
	s := openTest(t, t.TempDir())
	rekeyable(t, s)
	k, secrets := replacement()
	err := s.Do(context.Background(), func(tx *Tx) error { return tx.ReplaceDataKey(oldDEK, k, secrets[:1], testNow) })
	if err == nil {
		t.Fatal("ReplaceDataKey succeeded with an alert channel left behind")
	}
	s.Do(context.Background(), func(tx *Tx) error {
		if cur, err := tx.CurrentDataKey(); err != nil || cur.ID != oldDEK {
			t.Errorf("after the failure, CurrentDataKey = %+v, %v; want the old key", cur, err)
		}
		return nil
	})
}

func TestReplaceDataKeyFailsForAKeyThatIsNotCurrent(t *testing.T) {
	s := openTest(t, t.TempDir())
	rekeyable(t, s)
	k, secrets := replacement()
	err := s.Do(context.Background(), func(tx *Tx) error { return tx.ReplaceDataKey("dek_nosuch", k, secrets, testNow) })
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ReplaceDataKey of a key that is not current: %v, want ErrNotFound", err)
	}
}

func TestReplaceDataKeyFailsForASecretThatIsNotThere(t *testing.T) {
	s := openTest(t, t.TempDir())
	rekeyable(t, s)
	k, secrets := replacement()
	secrets = append(secrets, SealedSecret{Kind: SecretCredential, ID: "cr_gone", Ciphertext: newBlob("x"), Nonce: newBlob("y"), DataKeyID: newDEK})
	err := s.Do(context.Background(), func(tx *Tx) error { return tx.ReplaceDataKey(oldDEK, k, secrets, testNow) })
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ReplaceDataKey with a deleted credential: %v, want ErrNotFound", err)
	}
}

// An exclusive store zeroes what it replaces, so the old sealed bytes survive
// nowhere in the data directory: not in free pages, not in the WAL.
func TestAnExclusiveReplaceLeavesNoOldBytesInTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), Options{Dir: dir, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	rekeyable(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(readDir(t, dir), []byte("OLD-SEALED-BYTES")) {
		t.Fatal("the seeded bytes are not where this test looks, so it could prove nothing")
	}

	ex, err := OpenExclusive(context.Background(), Options{Dir: dir, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	k, secrets := replacement()
	if err := ex.Do(context.Background(), func(tx *Tx) error { return tx.ReplaceDataKey(oldDEK, k, secrets, testNow) }); err != nil {
		t.Fatal(err)
	}
	if err := ex.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(readDir(t, dir), []byte("OLD-SEALED-BYTES")) {
		t.Error("old sealed bytes survive in the data directory")
	}
}

func readDir(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var all []byte
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	return all
}
