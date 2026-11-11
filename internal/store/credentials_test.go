package store_test

import (
	"errors"
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

func TestKeysOnCredentialListsEveryKeyNotRevoked(t *testing.T) {
	s, u := withUpstream(t)
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.CreateCredential(credential("cr_1", "main", u.ID)); err != nil {
			return err
		}
		if _, err := tx.CreateCredential(credential("cr_2", "other", u.ID)); err != nil {
			return err
		}
		for _, k := range []struct{ id, credential, status string }{
			{"activeAA", "cr_1", store.KeyActive},
			{"disabled", "cr_1", store.KeyDisabled},
			{"revokedC", "cr_1", store.KeyRevoked},
			{"elsewher", "cr_2", store.KeyActive},
		} {
			key := virtualKey(k.id, k.id)
			key.CredentialID, key.Status = k.credential, k.status
			if _, err := tx.CreateKey(key, []byte(k.id)); err != nil {
				return err
			}
		}
		got, err := tx.KeysOnCredential("cr_1")
		if want := []string{"activeAA", "disabled"}; !slices.Equal(got, want) {
			t.Errorf("KeysOnCredential = %v, want %v", got, want)
		}
		return err
	})
}
