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
	"strings"
	"time"

	"modernc.org/sqlite" // also registers the "sqlite" driver; pure Go, so CGO_ENABLED=0 holds
	sqlite3 "modernc.org/sqlite/lib"
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

	exclusive bool // set by OpenExclusive
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
	return open(ctx, opts, migrations)
}

// OpenExclusive is Open for offline commands such as rekey: it fails with
// ErrHeld while anything else has the database open, a running server
// included, and keeps every other opener out until Close. The operating system
// drops the lock when a process dies, so a crash never leaves it behind.
//
// Its transactions are durable at commit, and what they delete or overwrite is
// zeroed in the file rather than left in free space, so a secret they replace
// is gone once Close has checkpointed the write-ahead log.
func OpenExclusive(ctx context.Context, opts Options) (*Store, error) {
	opts.exclusive = true
	return open(ctx, opts, migrations)
}

// open is Open with the migrations given, so tests can stand in later ones.
func open(ctx context.Context, opts Options, ms []migration) (*Store, error) {
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
	source := dsn(s.path)
	if opts.exclusive {
		source = exclusiveDSN(s.path)
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)
	s.db = db
	if err := s.start(ctx, opts.Now, ms, opts.exclusive); err != nil {
		db.Close()
		if isBusy(err) {
			return nil, fmt.Errorf("store: %s: %w", s.path, ErrHeld)
		}
		return nil, err
	}
	return s, nil
}

// start connects, migrates, and then tightens the files SQLite created. An
// exclusive store claims the database first, so it migrates nothing it does
// not hold.
func (s *Store) start(ctx context.Context, now time.Time, ms []migration, exclusive bool) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: open %s: %w", s.path, err)
	}
	if exclusive {
		if err := s.claim(ctx); err != nil {
			return err
		}
	}
	if err := s.migrate(ctx, now, ms); err != nil {
		return err
	}
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := s.tighten(p, fileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// claim takes the exclusive file lock. In locking_mode=EXCLUSIVE SQLite takes
// it when the first write transaction begins and never lets it go. Every other
// connection in WAL mode holds a shared lock for as long as it is open, even
// idle, so this is what fails while a server is running.
func (s *Store) claim(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: claim %s: %w", s.path, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: claim %s: %w", s.path, err)
	}
	return nil
}

// isBusy reports whether err is SQLite saying another connection holds a lock.
func isBusy(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_BUSY
}

// dsn is the connection string for the database at path.
func dsn(path string) string {
	return fileURI(path) + "?" + dsnParams().Encode()
}

// exclusiveDSN is dsn for OpenExclusive. A holder is refused quickly: nothing
// is about to let go of a database the operator forgot was in use.
func exclusiveDSN(path string) string {
	q := dsnParams()
	q.Set("_busy_timeout", "500")
	q.Set("_synchronous", "FULL")
	q.Add("_pragma", "locking_mode(EXCLUSIVE)")
	q.Add("_pragma", "secure_delete(ON)")
	return fileURI(path) + "?" + q.Encode()
}

// dsnParams are the connection settings. The shorthand keys are applied by the
// driver on every new connection, auto_vacuum first, so it takes effect before
// the first table exists. _txlock makes every Do take the write lock up front,
// so a transaction never fails to upgrade mid-way.
func dsnParams() url.Values {
	q := url.Values{}
	q.Set("_busy_timeout", "5000")
	q.Set("_auto_vacuum", "INCREMENTAL")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "NORMAL")
	q.Set("_foreign_keys", "1")
	q.Set("_txlock", "immediate")
	q.Add("_pragma", "trusted_schema(OFF)")
	q.Add("_pragma", "journal_size_limit(67108864)")
	return q
}

// fileURI is path as an SQLite file: URI, with the characters that would end
// or garble the path escaped.
func fileURI(path string) string {
	return "file:" + strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(path))
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
