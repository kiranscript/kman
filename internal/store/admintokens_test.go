package store_test

import (
	"errors"
	"testing"
	"time"

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

// hash32 is a distinct 32-byte stand-in for a token hash.
func hash32(c byte) []byte {
	h := make([]byte, 32)
	for i := range h {
		h[i] = c
	}
	return h
}

func createToken(t *testing.T, s *store.Store, name string, h []byte) store.AdminToken {
	t.Helper()
	var got store.AdminToken
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.CreateAdminToken(store.AdminToken{Name: name, Hash: h, CreatedAt: now})
		return err
	})
	return got
}

func TestAdminTokensListsEveryTokenByName(t *testing.T) {
	s := open(t)
	createToken(t, s, "owner", hash32(1))
	createToken(t, s, "ci", hash32(2))
	var got []store.AdminToken
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.AdminTokens()
		return err
	})
	if len(got) != 2 || got[0].Name != "ci" || got[1].Name != "owner" {
		t.Fatalf("AdminTokens = %+v, want ci then owner", got)
	}
	var one store.AdminToken
	do(t, s, func(tx *store.Tx) error {
		var err error
		one, err = tx.AdminTokenByName("owner")
		return err
	})
	if one.Name != "owner" || one.Hash[0] != 1 {
		t.Errorf("AdminTokenByName = %+v", one)
	}
}

func TestRotateAdminTokenReplacesTheHashAndKeepsTheName(t *testing.T) {
	s := open(t)
	before := createToken(t, s, "ci", hash32(1))
	var after store.AdminToken
	do(t, s, func(tx *store.Tx) error {
		var err error
		after, err = tx.RotateAdminToken("ci", hash32(2))
		return err
	})
	if after.Name != "ci" || after.Hash[0] != 2 || after.Generation <= before.Generation || !after.CreatedAt.Equal(now) {
		t.Errorf("RotateAdminToken = %+v, want ci with the new hash and a later generation", after)
	}
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.AdminTokenByHash(hash32(1))
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the old hash still finds a token: %v", err)
	}
}

func TestRevokeAdminTokenMarksItRevokedOnce(t *testing.T) {
	s := open(t)
	createToken(t, s, "ci", hash32(1))
	later := now.Add(time.Hour)
	var got store.AdminToken
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.RevokeAdminToken("ci", later)
		return err
	})
	if !got.RevokedAt.Equal(later) {
		t.Errorf("RevokedAt = %v, want %v", got.RevokedAt, later)
	}
	for name, fn := range map[string]func(*store.Tx) error{
		"revoke again":          func(tx *store.Tx) error { _, err := tx.RevokeAdminToken("ci", later); return err },
		"rotate a revoked":      func(tx *store.Tx) error { _, err := tx.RotateAdminToken("ci", hash32(3)); return err },
		"revoke a missing":      func(tx *store.Tx) error { _, err := tx.RevokeAdminToken("nope", later); return err },
		"rotate a missing":      func(tx *store.Tx) error { _, err := tx.RotateAdminToken("nope", hash32(3)); return err },
		"by name of a missing":  func(tx *store.Tx) error { _, err := tx.AdminTokenByName("nope"); return err },
		"touch a missing token": func(tx *store.Tx) error { return tx.TouchAdminToken("nope", later) },
	} {
		if err := s.Do(t.Context(), fn); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", name, err)
		}
	}
}

func TestTouchAdminTokenRecordsUseWithoutAGeneration(t *testing.T) {
	s := open(t)
	created := createToken(t, s, "owner", hash32(1))
	used := now.Add(time.Minute)
	var got store.AdminToken
	var m store.Meta
	do(t, s, func(tx *store.Tx) error {
		if err := tx.TouchAdminToken("owner", used); err != nil {
			return err
		}
		var err error
		if got, err = tx.AdminTokenByName("owner"); err != nil {
			return err
		}
		m, err = tx.Meta()
		return err
	})
	if !got.LastUsedAt.Equal(used) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, used)
	}
	// Use is not a change to the install.
	if got.Generation != created.Generation || m.Generation != created.Generation {
		t.Errorf("touch moved the generation: token %d, meta %d, want %d", got.Generation, m.Generation, created.Generation)
	}
}
