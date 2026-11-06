package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// withUpstream is a store holding a data key and one upstream, as a
// credential needs.
func withUpstream(t *testing.T) (*store.Store, store.Upstream) {
	t.Helper()
	s := open(t)
	var u store.Upstream
	do(t, s, func(tx *store.Tx) error {
		if err := tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: now}); err != nil {
			return err
		}
		var err error
		u, err = tx.CreateUpstream(openai("openai"))
		return err
	})
	return s, u
}

func credential(id, name, upstreamID string) store.Credential {
	return store.Credential{
		ID: id, UpstreamID: upstreamID, Name: name, Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"),
		DataKeyID: "dek_1", Last4: "abcd", CreatedAt: now,
	}
}

func TestACredentialIsStoredAndReadBackByIdAndName(t *testing.T) {
	s, u := withUpstream(t)
	var created store.Credential
	do(t, s, func(tx *store.Tx) error {
		var err error
		created, err = tx.CreateCredential(credential("cr_1", "main", u.ID))
		return err
	})
	if created.Generation == 0 || created.Generation == u.Generation {
		t.Errorf("generation = %d, want one of its own (the upstream has %d)", created.Generation, u.Generation)
	}
	do(t, s, func(tx *store.Tx) error {
		for _, read := range []func() (store.Credential, error){
			func() (store.Credential, error) { return tx.Credential("cr_1") },
			func() (store.Credential, error) { return tx.CredentialByName("main") },
		} {
			got, err := read()
			if err != nil {
				return err
			}
			if got.ID != "cr_1" || got.UpstreamID != u.ID || got.Name != "main" || string(got.Ciphertext) != "ciphertext" ||
				string(got.Nonce) != "nonce" || got.DataKeyID != "dek_1" || got.Last4 != "abcd" || !got.CreatedAt.Equal(now) ||
				!got.RotatedAt.IsZero() || got.Generation != created.Generation {
				t.Errorf("read back %+v, want %+v", got, created)
			}
		}
		return nil
	})
}

func TestCredentialNamesAreUniqueAmongLiveCredentials(t *testing.T) {
	s, u := withUpstream(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateCredential(credential("cr_1", "main", u.ID))
		return err
	})
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.CreateCredential(credential("cr_2", "main", u.ID))
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a second credential named main = %v, want ErrConflict", err)
	}
	do(t, s, func(tx *store.Tx) error {
		if err := tx.DeleteCredential("cr_1", now); err != nil {
			return err
		}
		_, err := tx.CreateCredential(credential("cr_2", "main", u.ID))
		return err
	})
}

func TestADeletedCredentialIsGone(t *testing.T) {
	s, u := withUpstream(t)
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.CreateCredential(credential("cr_1", "main", u.ID)); err != nil {
			return err
		}
		return tx.DeleteCredential("cr_1", now)
	})
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.Credential("cr_1"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Credential = %v, want ErrNotFound", err)
		}
		if _, err := tx.CredentialByName("main"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CredentialByName = %v, want ErrNotFound", err)
		}
		if err := tx.DeleteCredential("cr_1", now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("a second DeleteCredential = %v, want ErrNotFound", err)
		}
		all, err := tx.Credentials()
		if err != nil || len(all) != 0 {
			t.Errorf("Credentials = %+v, %v, want none", all, err)
		}
		return nil
	})
}

func TestCredentialsListsLiveOnesByName(t *testing.T) {
	s, u := withUpstream(t)
	do(t, s, func(tx *store.Tx) error {
		for _, c := range [][2]string{{"cr_1", "zulu"}, {"cr_2", "alpha"}, {"cr_3", "gone"}} {
			if _, err := tx.CreateCredential(credential(c[0], c[1], u.ID)); err != nil {
				return err
			}
		}
		return tx.DeleteCredential("cr_3", now)
	})
	do(t, s, func(tx *store.Tx) error {
		all, err := tx.Credentials()
		var names []string
		for _, c := range all {
			names = append(names, c.Name)
		}
		if want := []string{"alpha", "zulu"}; !slices.Equal(names, want) {
			t.Errorf("Credentials = %v, want %v", names, want)
		}
		return err
	})
}

func TestUpdateCredentialOverwritesTheCiphertext(t *testing.T) {
	s, u := withUpstream(t)
	var before store.Credential
	do(t, s, func(tx *store.Tx) error {
		var err error
		before, err = tx.CreateCredential(credential("cr_1", "main", u.ID))
		return err
	})
	rotated := now.Add(time.Hour)
	var after store.Credential
	do(t, s, func(tx *store.Tx) error {
		c := before
		c.Name, c.Ciphertext, c.Nonce, c.Last4, c.RotatedAt = "renamed", []byte("new"), []byte("new nonce"), "wxyz", rotated
		var err error
		after, err = tx.UpdateCredential(c)
		return err
	})
	if after.Name != "renamed" || string(after.Ciphertext) != "new" || string(after.Nonce) != "new nonce" || after.Last4 != "wxyz" ||
		!after.RotatedAt.Equal(rotated) || !after.CreatedAt.Equal(now) || after.Generation <= before.Generation {
		t.Errorf("UpdateCredential = %+v", after)
	}
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		c := after
		c.ID = "cr_missing"
		_, err := tx.UpdateCredential(c)
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("updating a missing credential = %v, want ErrNotFound", err)
	}
}

// seedKey writes a virtual key directly: until #36 no command makes one.
func seedKey(t *testing.T, dir, keyID, name, credentialID, status string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, store.FileName))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO virtual_keys (key_id, name, credential_id, kind, status, policy_version, created_at, generation)
		VALUES (?, ?, ?, 'standard', ?, 1, 0, 1)`, keyID, name, credentialID, status); err != nil {
		t.Fatal(err)
	}
}

func TestKeysOnCredentialListsEveryKeyNotRevoked(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.Context(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var u store.Upstream
	do(t, s, func(tx *store.Tx) error {
		if err := tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: now}); err != nil {
			return err
		}
		if u, err = tx.CreateUpstream(openai("openai")); err != nil {
			return err
		}
		if _, err := tx.CreateCredential(credential("cr_1", "main", u.ID)); err != nil {
			return err
		}
		_, err := tx.CreateCredential(credential("cr_2", "other", u.ID))
		return err
	})
	seedKey(t, dir, "activeAA", "a", "cr_1", "active")
	seedKey(t, dir, "disabled", "b", "cr_1", "disabled")
	seedKey(t, dir, "revokedC", "c", "cr_1", "revoked")
	seedKey(t, dir, "elsewher", "d", "cr_2", "active")
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.KeysOnCredential("cr_1")
		if want := []string{"activeAA", "disabled"}; !slices.Equal(got, want) {
			t.Errorf("KeysOnCredential = %v, want %v", got, want)
		}
		return err
	})
}
