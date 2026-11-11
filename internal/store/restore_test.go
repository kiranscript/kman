package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

// installWith makes a data directory whose one upstream is named name.
func installWith(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(t.Context(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	do(t, s, func(tx *store.Tx) error {
		if err := tx.CreateDataKey(store.DataKey{ID: "dek_" + name, Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: now}); err != nil {
			return err
		}
		_, err := tx.CreateUpstream(openai(name))
		return err
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// backupOf backs the install in dir up to a file and returns its path.
func backupOf(t *testing.T, dir string) string {
	t.Helper()
	s, err := store.Open(t.Context(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dst := filepath.Join(t.TempDir(), "kman.backup")
	if err := s.Backup(t.Context(), dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

// upstreamNames are the upstreams in the database in dir.
func upstreamNames(t *testing.T, dir string) []string {
	t.Helper()
	s, err := store.Open(t.Context(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var names []string
	do(t, s, func(tx *store.Tx) error {
		us, err := tx.Upstreams()
		for _, u := range us {
			names = append(names, u.Name)
		}
		return err
	})
	return names
}

func TestInspectBackupReadsWhatRestoreNeedsToKnow(t *testing.T) {
	src := installWith(t, "from-backup")
	info, err := store.InspectBackup(t.Context(), backupOf(t, src))
	if err != nil {
		t.Fatalf("InspectBackup: %v", err)
	}
	if info.SchemaVersion != store.SchemaVersion || info.InstallID == "" || info.DataKey.ID != "dek_from-backup" || info.DataKey.KEKFingerprint != "mkf_1" {
		t.Errorf("InspectBackup = %+v", info)
	}
}

func TestInspectBackupRefusesWhatIsNotAKmanDatabase(t *testing.T) {
	junk := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(junk, []byte("this is not a database, just text, long enough to have a header, "+string(make([]byte, 200))), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InspectBackup(t.Context(), junk); err == nil {
		t.Error("InspectBackup accepted a text file")
	}
	if _, err := store.InspectBackup(t.Context(), filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("InspectBackup of a missing file: %v, want ErrNotExist", err)
	}
	if _, err := store.InspectBackup(t.Context(), emptyDatabase(t)); err == nil {
		t.Error("InspectBackup accepted a database that is not kman's")
	}
}

func TestRestoreReplacesTheDatabaseAndKeepsTheOldOneAside(t *testing.T) {
	backup := backupOf(t, installWith(t, "from-backup"))
	dir := installWith(t, "current")
	if err := store.Restore(t.Context(), dir, backup); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := upstreamNames(t, dir); len(got) != 1 || got[0] != "from-backup" {
		t.Errorf("after the restore the upstreams are %v, want [from-backup]", got)
	}
	// What was there is not lost: it is the way back.
	aside := t.TempDir()
	copyFile(t, filepath.Join(dir, store.FileName+".before-restore"), filepath.Join(aside, store.FileName))
	if got := upstreamNames(t, aside); len(got) != 1 || got[0] != "current" {
		t.Errorf("the copy set aside holds %v, want [current]", got)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(dir, store.FileName)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("restored database mode = %v, %v, want 0600", fi.Mode(), err)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.restoring")); len(m) != 0 {
		t.Errorf("staging files left behind: %v", m)
	}
}

func TestRestoreIsRefusedWhileAServerHoldsTheDatabase(t *testing.T) {
	backup := backupOf(t, installWith(t, "from-backup"))
	dir := installWith(t, "current")
	held, err := store.Open(t.Context(), store.Options{Dir: dir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	do(t, held, func(tx *store.Tx) error { _, err := tx.Meta(); return err })

	if err := store.Restore(t.Context(), dir, backup); !errors.Is(err, store.ErrHeld) {
		t.Fatalf("Restore beside a running store: %v, want ErrHeld", err)
	}
	do(t, held, func(tx *store.Tx) error {
		us, err := tx.Upstreams()
		if err == nil && (len(us) != 1 || us[0].Name != "current") {
			t.Errorf("a refused restore changed the database: %v", us)
		}
		return err
	})
	if _, err := os.Stat(filepath.Join(dir, store.FileName+".before-restore")); err == nil {
		t.Error("a refused restore set the database aside")
	}
}

func TestRestoreRefusesABackupThatIsNotOneAndChangesNothing(t *testing.T) {
	dir := installWith(t, "current")
	junk := filepath.Join(t.TempDir(), "junk")
	os.WriteFile(junk, make([]byte, 4096), 0o600)
	if err := store.Restore(t.Context(), dir, junk); err == nil {
		t.Fatal("Restore accepted a file of zeros")
	}
	if got := upstreamNames(t, dir); len(got) != 1 || got[0] != "current" {
		t.Errorf("a refused restore changed the database: %v", got)
	}
}

func TestRestoreWorksWhereThereIsNoDatabaseYet(t *testing.T) {
	backup := backupOf(t, installWith(t, "from-backup"))
	dir := filepath.Join(t.TempDir(), "fresh", "data")
	if err := store.Restore(t.Context(), dir, backup); err != nil {
		t.Fatalf("Restore into an empty place: %v", err)
	}
	if got := upstreamNames(t, dir); len(got) != 1 || got[0] != "from-backup" {
		t.Errorf("upstreams = %v, want [from-backup]", got)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("data directory mode = %v, %v, want 0700", fi.Mode(), err)
		}
	}
}

func TestRestoreReplacesADamagedDatabase(t *testing.T) {
	backup := backupOf(t, installWith(t, "from-backup"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, store.FileName), []byte("damaged, not a database at all"+string(make([]byte, 4096))), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Restore(t.Context(), dir, backup); err != nil {
		t.Fatalf("Restore over a damaged database: %v", err)
	}
	if got := upstreamNames(t, dir); len(got) != 1 || got[0] != "from-backup" {
		t.Errorf("upstreams = %v, want [from-backup]", got)
	}
}

func TestRestoreLeavesNothingOfTheOldWriteAheadLog(t *testing.T) {
	backup := backupOf(t, installWith(t, "from-backup"))
	dir := installWith(t, "current")
	// A crash leaves a log behind; the old database is recovered before it is set aside.
	if err := os.WriteFile(filepath.Join(dir, store.FileName+"-wal"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Restore(context.Background(), dir, backup); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if fi, err := os.Stat(filepath.Join(dir, store.FileName+suffix)); err == nil && fi.Size() != 0 {
			t.Errorf("%s%s is left beside the restored database, %d bytes", store.FileName, suffix, fi.Size())
		}
	}
}

// emptyDatabase is a valid SQLite file that holds no kman tables.
func emptyDatabase(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "other.db")
	db, err := sql.Open("sqlite", p) // the driver the store registers
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE other (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	return p
}
