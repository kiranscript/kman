package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The pragmas are per connection, so they are read through the store's own
// connection rather than a second one.
func TestOpenSetsThePragmas(t *testing.T) {
	s := openTest(t, t.TempDir())
	want := map[string]string{
		"journal_mode":       "wal",
		"synchronous":        "1", // NORMAL
		"busy_timeout":       "5000",
		"foreign_keys":       "1",
		"trusted_schema":     "0",
		"auto_vacuum":        "2", // INCREMENTAL
		"journal_size_limit": "67108864",
	}
	for pragma, v := range want {
		var got string
		if err := s.db.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if got != v {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, v)
		}
	}
}

func TestOpenCreatesPrivateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "data")
	openTest(t, dir)
	assertMode(t, dir, 0o700)
	for _, name := range []string{"kman.db", "kman.db-wal", "kman.db-shm"} {
		assertMode(t, filepath.Join(dir, name), 0o600)
	}
}

func TestOpenTightensLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir := t.TempDir()
	s := openTest(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "kman.db"), 0o644); err != nil {
		t.Fatal(err)
	}
	openTest(t, dir)
	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, "kman.db"), 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("%s has mode %v, want %v", filepath.Base(path), got, want)
	}
}

// Each of '#', '?' and '%' would end or change the path inside an SQLite
// file: URI. Windows forbids '?' in a file name, so it is tested elsewhere.
func TestOpenAcceptsAnAwkwardDirectoryName(t *testing.T) {
	name := "a #b?c%20d"
	if runtime.GOOS == "windows" {
		name = "a #b c%20d"
	}
	dir := filepath.Join(t.TempDir(), name)
	openTest(t, dir)
	if _, err := os.Stat(filepath.Join(dir, FileName+"-wal")); err != nil {
		t.Errorf("SQLite did not open the database at the given path: %v", err)
	}
}
