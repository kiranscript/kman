package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Tx is one store transaction, handed to the function given to Do. Its
// methods are the typed repos. A Tx must not be used after that function
// returns.
type Tx struct {
	ctx  context.Context
	tx   *sql.Tx
	rand io.Reader
	gen  int64 // this transaction's generation, once taken; 0 before
}

// Do runs fn in one transaction. It commits if fn returns nil, and rolls back
// if fn returns an error, which Do returns unchanged, or panics, which Do
// re-panics after the rollback. Transactions run one at a time.
func (s *Store) Do(ctx context.Context, fn func(*Tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			sqlTx.Rollback()
		}
	}()
	if err := fn(&Tx{ctx: ctx, tx: sqlTx, rand: s.rand}); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	committed = true
	return nil
}

// Generation returns this transaction's generation number. The first call
// takes the next value of the install-wide counter; later calls in the same
// transaction return the same value. Every row the transaction changes is
// stamped with it, so a transaction that changes nothing takes none, and a
// rolled-back transaction gives its number back.
func (tx *Tx) Generation() (int64, error) {
	if tx.gen != 0 {
		return tx.gen, nil
	}
	err := tx.tx.QueryRowContext(tx.ctx,
		`UPDATE meta SET generation = generation + 1 WHERE id = 1 RETURNING generation`).Scan(&tx.gen)
	if err != nil {
		return 0, fmt.Errorf("store: take generation: %w", err)
	}
	return tx.gen, nil
}

// Meta is the install's one meta row.
type Meta struct {
	InstallID string
	CreatedAt time.Time
	// Generation is the last generation taken, by this transaction or an
	// earlier one.
	Generation int64
}

// Meta returns the install's meta row.
func (tx *Tx) Meta() (Meta, error) {
	var m Meta
	var created int64
	err := tx.tx.QueryRowContext(tx.ctx,
		`SELECT install_id, created_at, generation FROM meta WHERE id = 1`).Scan(&m.InstallID, &created, &m.Generation)
	if err != nil {
		return Meta{}, fmt.Errorf("store: read meta: %w", err)
	}
	m.CreatedAt = fromMilli(created)
	return m, nil
}

// fromMilli turns a stored Unix-millisecond timestamp into a UTC time.
func fromMilli(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// toMilli is t as stored: Unix milliseconds.
func toMilli(t time.Time) int64 { return t.UnixMilli() }

// typed turns a driver error into one of the store's typed errors where one
// applies: no row is ErrNotFound and a uniqueness clash is ErrConflict. what
// names the thing, for the message.
func typed(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: %s: %w", what, ErrNotFound)
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("store: %s: %w", what, ErrConflict)
		}
	}
	return fmt.Errorf("store: %s: %w", what, err)
}
