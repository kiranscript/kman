package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestGenerateIsDeterministic(t *testing.T) {
	a := generateTo(t, t.TempDir(), "v0.1.0")
	b := generateTo(t, t.TempDir(), "v0.1.0")
	x, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(x, y) {
		t.Error("two runs wrote different fixtures")
	}
}

func TestGenerateWritesAnOpenableInstall(t *testing.T) {
	path := generateTo(t, t.TempDir(), "v0.1.0")
	if filepath.Base(path) != "v0.1.0.db" {
		t.Errorf("fixture is %s, want v0.1.0.db", filepath.Base(path))
	}
	dir := t.TempDir()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, store.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), store.Options{Dir: dir, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.Do(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.UpstreamByName("openai"); err != nil {
			return err
		}
		entries, err := tx.AuditEntries(0, 100)
		if len(entries) == 0 {
			return errors.New("no audit entries")
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
}

func TestGenerateRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	generateTo(t, dir, "v0.1.0")
	if err := generate(context.Background(), dir, "v0.1.0"); err == nil {
		t.Error("a second generate over the same fixture succeeded")
	}
}

func TestGenerateRefusesABadVersion(t *testing.T) {
	for _, v := range []string{"", "0.1.0", "v0.1", "v0.1.0/../x", "latest"} {
		if err := generate(context.Background(), t.TempDir(), v); err == nil {
			t.Errorf("generate with version %q succeeded", v)
		}
	}
}

func generateTo(t *testing.T, dir, version string) string {
	t.Helper()
	if err := generate(context.Background(), dir, version); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return filepath.Join(dir, version+".db")
}
