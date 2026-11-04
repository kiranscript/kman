package store_test

import (
	"errors"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestCreateAdminTokenStampsAGeneration(t *testing.T) {
	s := open(t)
	var got store.AdminToken
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.CreateAdminToken(store.AdminToken{Name: "owner", Hash: make([]byte, 32), CreatedAt: now})
		return err
	})
	if got.Name != "owner" || got.Generation == 0 || !got.CreatedAt.Equal(now) {
		t.Errorf("CreateAdminToken = %+v, want owner with a generation", got)
	}
	var back store.AdminToken
	do(t, s, func(tx *store.Tx) error {
		var err error
		back, err = tx.AdminTokenByHash(make([]byte, 32))
		return err
	})
	if back.Name != "owner" || back.Generation != got.Generation {
		t.Errorf("AdminTokenByHash = %+v, want %+v", back, got)
	}
}

func TestCreateAdminTokenRefusesATakenName(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateAdminToken(store.AdminToken{Name: "owner", Hash: make([]byte, 32), CreatedAt: now})
		return err
	})
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.CreateAdminToken(store.AdminToken{Name: "owner", Hash: []byte("another hash, 32 bytes long....."), CreatedAt: now})
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a second owner = %v, want ErrConflict", err)
	}
}

func TestAdminTokenByHashOfAnUnknownHashIsNotFound(t *testing.T) {
	s := open(t)
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.AdminTokenByHash(make([]byte, 32))
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AdminTokenByHash = %v, want ErrNotFound", err)
	}
}
