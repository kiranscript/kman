package store_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// withCredential opens a store with one upstream and a credential, cr_1.
func withCredential(t *testing.T) *store.Store {
	t.Helper()
	s, u := withUpstream(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateCredential(credential("cr_1", "main", u.ID))
		return err
	})
	return s
}

func virtualKey(id, name string) store.VirtualKey {
	return store.VirtualKey{ID: id, Name: name, CredentialID: "cr_1", PolicyJSON: `{}`, PolicyVersion: 1,
		TagsJSON: `[]`, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
}

var hash = bytes.Repeat([]byte{7}, 32)

func TestACreatedKeyReadsBackWithItsCurrentSecret(t *testing.T) {
	s := withCredential(t)
	k := virtualKey("abcd1234", "my-app")
	k.Note, k.TagsJSON = "billing", `["prod"]`
	do(t, s, func(tx *store.Tx) error {
		created, err := tx.CreateKey(k, hash)
		if err != nil {
			return err
		}
		if created.Generation == 0 || created.Status != store.KeyActive || created.Kind != store.KeyStandard {
			t.Errorf("CreateKey = %+v", created)
		}
		got, err := tx.Key("abcd1234")
		if err != nil {
			return err
		}
		if got != created || got.Name != "my-app" || got.Note != "billing" || got.TagsJSON != `["prod"]` ||
			got.CredentialID != "cr_1" || !got.ExpiresAt.Equal(k.ExpiresAt) || !got.CreatedAt.Equal(now) {
			t.Errorf("Key = %+v, created %+v", got, created)
		}
		secrets, err := tx.KeySecrets("abcd1234")
		if err != nil {
			return err
		}
		if len(secrets) != 1 || !bytes.Equal(secrets[0].Hash, hash) || secrets[0].State != store.SecretCurrent || !secrets[0].CreatedAt.Equal(now) {
			t.Errorf("KeySecrets = %+v", secrets)
		}
		return nil
	})
}

func TestAMissingKeyIsNotFound(t *testing.T) {
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.Key("nothere0"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Key = %v", err)
		}
		if _, err := tx.KeyByName("nothere"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("KeyByName = %v", err)
		}
		if _, err := tx.UpdateKey(virtualKey("nothere0", "x")); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UpdateKey = %v", err)
		}
		return nil
	})
}

func TestAKeyNameIsOptionalAndUniqueAmongKeysNotRevoked(t *testing.T) {
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		for _, k := range []store.VirtualKey{virtualKey("noname01", ""), virtualKey("noname02", ""), virtualKey("named001", "app")} {
			if _, err := tx.CreateKey(k, append([]byte(k.ID), hash...)); err != nil {
				return err
			}
		}
		return nil
	})
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.CreateKey(virtualKey("named002", "app"), append([]byte("named002"), hash...))
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Errorf("a second key named app = %v, want ErrConflict", err)
	}
	do(t, s, func(tx *store.Tx) error {
		k, err := tx.KeyByName("app")
		if err != nil {
			return err
		}
		if k.ID != "named001" {
			t.Errorf("KeyByName = %+v", k)
		}
		unnamed, err := tx.Key("noname01")
		if err != nil {
			return err
		}
		if unnamed.Name != "" {
			t.Errorf("an unnamed key reads back as %q", unnamed.Name)
		}
		k.Status = store.KeyRevoked
		if _, err := tx.UpdateKey(k); err != nil {
			return err
		}
		if _, err := tx.KeyByName("app"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("KeyByName of a revoked key = %v", err)
		}
		// The revoked key's name is free again.
		_, err = tx.CreateKey(virtualKey("named003", "app"), append([]byte("named003"), hash...))
		return err
	})
}

func TestUpdateKeyChangesItsFieldsAndStampsTheGeneration(t *testing.T) {
	s := withCredential(t)
	var created store.VirtualKey
	do(t, s, func(tx *store.Tx) error {
		var err error
		created, err = tx.CreateKey(virtualKey("abcd1234", "my-app"), hash)
		return err
	})
	later := now.Add(48 * time.Hour)
	do(t, s, func(tx *store.Tx) error {
		k := created
		k.Name, k.Note, k.TagsJSON, k.PolicyJSON, k.PolicyVersion, k.ExpiresAt = "", "moved", `["a"]`, `{"x":1}`, 2, later
		got, err := tx.UpdateKey(k)
		if err != nil {
			return err
		}
		if got.Generation <= created.Generation || got.Name != "" || got.Note != "moved" || got.TagsJSON != `["a"]` ||
			got.PolicyJSON != `{"x":1}` || got.PolicyVersion != 2 || !got.ExpiresAt.Equal(later) || !got.CreatedAt.Equal(now) {
			t.Errorf("UpdateKey = %+v", got)
		}
		return nil
	})
}

func TestKeysListsEveryKeyOldestFirst(t *testing.T) {
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		second := virtualKey("zzzz0002", "b")
		second.CreatedAt = now.Add(time.Minute)
		revoked := virtualKey("aaaa0003", "c")
		revoked.CreatedAt = now.Add(2 * time.Minute)
		for _, k := range []store.VirtualKey{second, virtualKey("bbbb0001", "a"), revoked} {
			if _, err := tx.CreateKey(k, append([]byte(k.ID), hash...)); err != nil {
				return err
			}
		}
		revoked.Status = store.KeyRevoked
		if _, err := tx.UpdateKey(revoked); err != nil {
			return err
		}
		keys, err := tx.Keys()
		if err != nil {
			return err
		}
		var ids []string
		for _, k := range keys {
			ids = append(ids, k.ID)
		}
		if want := []string{"bbbb0001", "zzzz0002", "aaaa0003"}; !slices.Equal(ids, want) {
			t.Errorf("Keys = %v, want %v", ids, want)
		}
		return nil
	})
}
