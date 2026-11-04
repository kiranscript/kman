package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// addTable is a stand-in later migration.
func addTable(name string) migration {
	return func(ctx context.Context, tx *sql.Tx, _ migrationEnv) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE "+name+" (x INTEGER) STRICT")
		return err
	}
}

var laterMigrations = []migration{migrate0001, addTable("m2"), addTable("m3"), addTable("m4")}

// openWith opens the store in dir knowing only the first n migrations.
func openWith(t *testing.T, dir string, n int) (*Store, error) {
	t.Helper()
	s, err := open(context.Background(), Options{Dir: dir, Now: testNow}, laterMigrations[:n])
	if err == nil {
		t.Cleanup(func() { s.Close() })
	}
	return s, err
}

func mustOpenWith(t *testing.T, dir string, n int) *Store {
	t.Helper()
	s, err := openWith(t, dir, n)
	if err != nil {
		t.Fatalf("open with %d migrations: %v", n, err)
	}
	return s
}

func preCopies(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, FileName+".pre-*"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range m {
		m[i] = filepath.Base(m[i])
	}
	slices.Sort(m)
	return m
}

func TestNewDatabaseTakesNoCopy(t *testing.T) {
	dir := t.TempDir()
	mustOpenWith(t, dir, 1)
	if got := preCopies(t, dir); len(got) != 0 {
		t.Errorf("copies = %v, want none", got)
	}
}

func TestMigratingCopiesTheDatabaseFirst(t *testing.T) {
	dir := t.TempDir()
	s := mustOpenWith(t, dir, 1)
	appendTestAudit(t, s, 1000)
	s.Close()

	s = mustOpenWith(t, dir, 2)
	if got := userVersion(t, s); got != 2 {
		t.Errorf("user_version = %d, want 2", got)
	}
	if got, want := preCopies(t, dir), []string{"kman.db.pre-0002"}; !slices.Equal(got, want) {
		t.Fatalf("copies = %v, want %v", got, want)
	}
	copyPath := filepath.Join(dir, "kman.db.pre-0002")
	if runtime.GOOS != "windows" {
		assertMode(t, copyPath, 0o600)
	}
	// The copy is the database as it was: schema 1, with its audit entry.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(copyPath)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v, n int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM audit_log").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if v != 1 || n != 1 {
		t.Errorf("copy has user_version %d and %d audit entries, want 1 and 1", v, n)
	}
}

func TestOnlyTheLastTwoCopiesAreKept(t *testing.T) {
	dir := t.TempDir()
	for n := 1; n <= 4; n++ {
		s := mustOpenWith(t, dir, n)
		s.Close()
	}
	// Reopening at the current version takes no copy.
	mustOpenWith(t, dir, 4).Close()
	if got, want := preCopies(t, dir), []string{"kman.db.pre-0003", "kman.db.pre-0004"}; !slices.Equal(got, want) {
		t.Errorf("copies = %v, want %v", got, want)
	}
}

func TestAFailedMigrationLeavesTheSchemaAsItWas(t *testing.T) {
	dir := t.TempDir()
	mustOpenWith(t, dir, 1).Close()
	failing := func(ctx context.Context, tx *sql.Tx, _ migrationEnv) error {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE half (x INTEGER) STRICT"); err != nil {
			return err
		}
		return errors.New("boom")
	}
	if _, err := open(context.Background(), Options{Dir: dir, Now: testNow}, []migration{migrate0001, failing}); err == nil {
		t.Fatal("open with a failing migration succeeded")
	}
	s := mustOpenWith(t, dir, 1)
	if got := userVersion(t, s); got != 1 {
		t.Errorf("user_version = %d, want 1", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name = 'half'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the failed migration's table exists")
	}
}

func TestANewerSchemaIsRefused(t *testing.T) {
	dir := t.TempDir()
	mustOpenWith(t, dir, 2).Close()
	_, err := openWith(t, dir, 1)
	if !errors.Is(err, ErrNewerSchema) {
		t.Errorf("open = %v, want ErrNewerSchema", err)
	}
}

func TestReopeningKeepsTheInstall(t *testing.T) {
	dir := t.TempDir()
	var first, second Meta
	s := mustOpenWith(t, dir, 1)
	s.Do(context.Background(), func(tx *Tx) (err error) { first, err = tx.Meta(); return })
	s.Close()
	s = mustOpenWith(t, dir, 1)
	s.Do(context.Background(), func(tx *Tx) (err error) { second, err = tx.Meta(); return })
	if first.InstallID == "" || first != second {
		t.Errorf("meta changed across a reopen: %+v then %+v", first, second)
	}
	if !first.CreatedAt.Equal(testNow) {
		t.Errorf("created_at = %v, want %v", first.CreatedAt, testNow)
	}
}
