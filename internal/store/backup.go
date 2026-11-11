package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// keepCopies is how many pre-migration copies are kept.
const keepCopies = 2

// copyPrefix starts the name of each pre-migration copy:
// kman.db.pre-<version>, where version is the schema the database is about
// to be migrated to, as four digits (kman.db.pre-0002 is the database as it
// was before migration 0002).
const copyPrefix = FileName + ".pre-"

// copyAside writes a consistent copy of the database, as it is before being
// migrated to schema version to, then removes all but the newest keepCopies
// copies.
func (s *Store) copyAside(ctx context.Context, to int) error {
	dst := filepath.Join(filepath.Dir(s.path), fmt.Sprintf("%s%04d", copyPrefix, to))
	// A copy left by an earlier failed attempt at the same upgrade is
	// replaced: the database has not changed since it was taken.
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: copy before migrating: %w", err)
	}
	if err := s.Backup(ctx, dst); err != nil {
		return fmt.Errorf("store: copy before migrating: %w", err)
	}
	s.log.Info("copied the database before migrating", "copy", dst)
	return s.pruneCopies()
}

// Backup writes a consistent copy of the database to dst while the store is in
// use: a VACUUM INTO, which reads one snapshot and so never sees half of a
// transaction. The copy holds what the database holds, which is ciphertext only,
// so it opens only with the master key. dst is created 0600 and must not exist;
// a Backup that fails removes what it made.
func (s *Store) Backup(ctx context.Context, dst string) error {
	// VACUUM INTO accepts an empty file, so the copy is created 0600 first.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("store: back up: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("store: back up: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		os.Remove(dst)
		return fmt.Errorf("store: back up: %w", err)
	}
	return nil
}

// pruneCopies removes all but the newest keepCopies pre-migration copies.
func (s *Store) pruneCopies() error {
	dir := filepath.Dir(s.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("store: list copies: %w", err)
	}
	var versions []int
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), copyPrefix)
		if !ok || len(rest) != 4 {
			continue
		}
		if v, err := strconv.Atoi(rest); err == nil {
			versions = append(versions, v)
		}
	}
	slices.Sort(versions)
	for len(versions) > keepCopies {
		old := filepath.Join(dir, fmt.Sprintf("%s%04d", copyPrefix, versions[0]))
		if err := os.Remove(old); err != nil {
			return fmt.Errorf("store: remove old copy: %w", err)
		}
		versions = versions[1:]
	}
	return nil
}

// beforeRestoreSuffix names the database a restore sets aside:
// kman.db.before-restore.
const beforeRestoreSuffix = ".before-restore"

// stagingSuffix names the copy a restore makes beside the database before it
// swaps it in.
const stagingSuffix = ".restoring"

// BackupInfo is what InspectBackup learns of a backup without opening it for
// use.
type BackupInfo struct {
	SchemaVersion int
	InstallID     string
	// DataKey is the backup's current data key, wrapped under the master key
	// the backup needs.
	DataKey DataKey
}

// InspectBackup reads a backup file, or a pre-migration copy, without changing
// it and without migrating it. It refuses a file that is not a kman database,
// one that is damaged, and one a newer kman made (ErrNewerSchema).
func InspectBackup(ctx context.Context, path string) (BackupInfo, error) {
	if _, err := os.Stat(path); err != nil {
		return BackupInfo{}, fmt.Errorf("store: %w", err)
	}
	// immutable: the file is read as it is, with no -wal or -shm made beside it.
	db, err := sql.Open("sqlite", fileURI(path)+"?mode=ro&immutable=1")
	if err != nil {
		return BackupInfo{}, fmt.Errorf("store: read backup: %w", err)
	}
	defer db.Close()
	notKman := func(err error) error {
		return fmt.Errorf("store: %s is not a kman database: %w", path, err)
	}
	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&check); err != nil {
		return BackupInfo{}, notKman(err)
	}
	if check != "ok" {
		return BackupInfo{}, fmt.Errorf("store: %s is damaged: %s", path, check)
	}
	var info BackupInfo
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&info.SchemaVersion); err != nil {
		return BackupInfo{}, notKman(err)
	}
	if info.SchemaVersion > SchemaVersion {
		return BackupInfo{}, fmt.Errorf("store: %s: %w: it is at schema %d and this kman knows up to %d",
			path, ErrNewerSchema, info.SchemaVersion, SchemaVersion)
	}
	if info.SchemaVersion == 0 {
		return BackupInfo{}, notKman(errors.New("it has no schema"))
	}
	if err := db.QueryRowContext(ctx, `SELECT install_id FROM meta WHERE id = 1`).Scan(&info.InstallID); err != nil {
		return BackupInfo{}, notKman(err)
	}
	var created int64
	err = db.QueryRowContext(ctx, `SELECT dek_id, wrapped, nonce, kek_fingerprint, created_at FROM data_keys WHERE retired_at IS NULL`).
		Scan(&info.DataKey.ID, &info.DataKey.Wrapped, &info.DataKey.Nonce, &info.DataKey.KEKFingerprint, &created)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("store: %s holds no current data key, so no master key could open it: %w", path, err)
	}
	info.DataKey.CreatedAt = fromMilli(created)
	return info, nil
}

// Restore replaces the database in dir with the backup at src, with nothing
// else holding the database: it fails with ErrHeld beside a running server, and
// changes nothing. The database it replaces is set aside as
// kman.db.before-restore, replacing an earlier one, so a restore can be undone;
// a database too damaged to open is replaced just the same. The backup is read
// as it is and not migrated: the next Open does that, from a copy.
//
// Restore does not know the master key. Whether the backup opens with it is for
// the caller to ask, with InspectBackup's DataKey, before it restores.
func Restore(ctx context.Context, dir, src string) error {
	if _, err := InspectBackup(ctx, src); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("store: restore: %w", err)
	}
	path := filepath.Join(dir, FileName)
	if _, err := os.Stat(path); err == nil {
		if err := ensureNotHeld(ctx, path); err != nil {
			return err
		}
	}
	staging := path + stagingSuffix
	if err := copyPrivate(src, staging); err != nil {
		os.Remove(staging)
		return fmt.Errorf("store: restore: %w", err)
	}
	aside := path + beforeRestoreSuffix
	hadOld := false
	if err := os.Rename(path, aside); err == nil {
		hadOld = true
	} else if !errors.Is(err, os.ErrNotExist) {
		os.Remove(staging)
		return fmt.Errorf("store: restore: set the database aside: %w", err)
	}
	// ensureNotHeld left no log of the old database: SQLite folded it in as
	// the last connection closed. Whatever remains belongs to nothing.
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(path + suffix)
	}
	if err := os.Rename(staging, path); err != nil {
		if hadOld {
			os.Rename(aside, path)
		}
		os.Remove(staging)
		return fmt.Errorf("store: restore: %w", err)
	}
	return nil
}

// ensureNotHeld fails with ErrHeld if anything else has the database open. A
// database that cannot be opened at all is not held, and is for Restore to
// replace.
func ensureNotHeld(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", exclusiveDSN(path))
	if err != nil {
		return nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err == nil {
		err = tx.Commit()
	}
	if isBusy(err) {
		return fmt.Errorf("store: %s: %w", path, ErrHeld)
	}
	return nil
}

// copyPrivate copies src to dst, which is made 0600, and syncs it.
func copyPrivate(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
