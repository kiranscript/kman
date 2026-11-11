package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestBackupIsAConsistentCopyThatOpensWithTheSameData(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.Context(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateUpstream(openai("openai"))
		return err
	})

	dst := filepath.Join(t.TempDir(), "kman.backup")
	if err := s.Backup(t.Context(), dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	// The server keeps running: a write after the backup is not in it.
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateUpstream(openai("later"))
		return err
	})

	bdir := t.TempDir()
	copyFile(t, dst, filepath.Join(bdir, store.FileName))
	b, err := store.Open(t.Context(), store.Options{Dir: bdir, Now: now})
	if err != nil {
		t.Fatalf("opening the backup: %v", err)
	}
	defer b.Close()
	var names []string
	do(t, b, func(tx *store.Tx) error {
		us, err := tx.Upstreams()
		for _, u := range us {
			names = append(names, u.Name)
		}
		return err
	})
	if len(names) != 1 || names[0] != "openai" {
		t.Errorf("the backup holds upstreams %v, want [openai]", names)
	}
}

func TestBackupCarriesTheSealedCredentialsAsTheyAre(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.Context(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	do(t, s, func(tx *store.Tx) error {
		if err := tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("wrapped"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: now}); err != nil {
			return err
		}
		u, err := tx.CreateUpstream(openai("openai"))
		if err != nil {
			return err
		}
		c := credential("cr_1", "main", u.ID)
		c.Ciphertext = []byte("sealed-bytes-not-the-secret")
		_, err = tx.CreateCredential(c)
		return err
	})
	dst := filepath.Join(t.TempDir(), "kman.backup")
	if err := s.Backup(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("sealed-bytes-not-the-secret")) {
		t.Error("the backup lacks the sealed credential: it is not a copy of the database")
	}
}

func TestBackupRefusesToOverwriteAFile(t *testing.T) {
	s := open(t)
	dst := filepath.Join(t.TempDir(), "kman.backup")
	if err := os.WriteFile(dst, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := s.Backup(t.Context(), dst)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("Backup over an existing file: %v, want ErrExist", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "precious" {
		t.Errorf("the existing file was changed to %q", got)
	}
}

func TestBackupIsOwnerOnlyAndAFailedOneLeavesNoFile(t *testing.T) {
	s := open(t)
	dst := filepath.Join(t.TempDir(), "kman.backup")
	if err := s.Backup(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(dst); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("backup mode = %v, %v, want 0600", fi.Mode(), err)
		}
	}
	// A cancelled backup removes what it made.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	gone := filepath.Join(t.TempDir(), "gone.backup")
	if err := s.Backup(ctx, gone); err == nil {
		t.Error("a cancelled Backup succeeded")
	}
	if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed Backup left %s behind (%v)", gone, err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
