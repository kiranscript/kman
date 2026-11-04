package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver; pure Go, so CGO_ENABLED=0 holds
)

// FileName is the database file inside the data directory.
const FileName = "kman.db"

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// Options configures Open.
type Options struct {
	// Dir is the data directory. It is created 0700 if missing.
	Dir string
	// Now is the time Open runs at. A new install's meta row records it.
	Now time.Time
	// Rand is the source of random ids. It defaults to crypto/rand; the
	// fixture generator passes a seeded one so its output is reproducible.
	Rand io.Reader
	// Logger receives notices such as tightened permissions. It defaults to
	// discarding them.
	Logger *slog.Logger
}

// Store is kman's SQLite database. It holds one writer connection, so every
// Do runs one at a time.
type Store struct {
	db   *sql.DB
	path string
	rand io.Reader
	log  *slog.Logger
}

// Open opens or creates the database in opts.Dir, tightens the permissions of
// the directory and its files, copies the database aside if it needs a
// migration, and runs every pending migration.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Dir == "" {
		return nil, errors.New("store: no data directory")
	}
	if opts.Now.IsZero() {
		return nil, errors.New("store: no time given")
	}
	s := &Store{path: filepath.Join(opts.Dir, FileName), rand: opts.Rand, log: opts.Logger}
	if s.rand == nil {
		s.rand = rand.Reader
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(opts.Dir, dirMode); err != nil {
		return nil, fmt.Errorf("store: create data directory: %w", err)
	}
	if err := s.tighten(opts.Dir, dirMode); err != nil {
		return nil, err
	}
	// SQLite gives the -wal and -shm files the database file's mode, so the
	// database is created 0600 before SQLite first opens it.
	f, err := os.OpenFile(s.path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("store: create database: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: create database: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(s.path))
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)
	s.db = db
	if err := s.start(ctx, opts.Now); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// start connects, migrates, and then tightens the files SQLite created.
func (s *Store) start(ctx context.Context, now time.Time) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: open %s: %w", s.path, err)
	}
	if err := s.migrate(ctx, now, migrations); err != nil {
		return err
	}
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := s.tighten(p, fileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// dsn is the connection string for the database at path. The shorthand keys
// are applied by the driver on every new connection, auto_vacuum first, so it
// takes effect before the first table exists. _txlock makes every Do take the
// write lock up front, so a transaction never fails to upgrade mid-way.
func dsn(path string) string {
	q := url.Values{}
	q.Set("_busy_timeout", "5000")
	q.Set("_auto_vacuum", "INCREMENTAL")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "NORMAL")
	q.Set("_foreign_keys", "1")
	q.Set("_txlock", "immediate")
	q.Add("_pragma", "trusted_schema(OFF)")
	q.Add("_pragma", "journal_size_limit(67108864)")
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}

// tighten removes group and other permissions from path, logging when it had
// to. Windows has no such bits, so it does nothing there.
func (s *Store) tighten(path string, want os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if fi.Mode().Perm() == want {
		return nil
	}
	if err := os.Chmod(path, want); err != nil {
		return fmt.Errorf("store: tighten permissions: %w", err)
	}
	s.log.Warn("tightened permissions", "path", path, "was", fi.Mode().Perm().String(), "now", want.String())
	return nil
}

// Close closes the database. SQLite checkpoints the WAL into the database
// file as the last connection closes.
func (s *Store) Close() error {
	return s.db.Close()
}
