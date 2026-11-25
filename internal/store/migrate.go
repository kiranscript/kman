package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"
)

// A migration moves the schema forward by one version, inside one
// transaction. Migrations are Go so that one can compute values, such as the
// install id, in Go rather than in SQL.
type migration func(ctx context.Context, tx *sql.Tx, env migrationEnv) error

// migrationEnv is what a migration may use besides the transaction.
type migrationEnv struct {
	now  time.Time
	rand io.Reader
}

// SchemaVersion is the schema this kman migrates every database to: the
// number of migrations.
const SchemaVersion = 2

// migrations are applied in order: migrations[i] takes user_version from i to
// i+1. Once a release is tagged, its migrations never change; the schema
// moves on only by appending a migration.
var migrations = []migration{migrate0001, migrate0002}

// schemaPart is something a migration creates: a table, an index (named in
// table, with no column), or one column of a table that an earlier migration
// made.
type schemaPart struct{ table, column string }

// unreleased is what each migration that is not in a tagged release creates,
// by version. Such a migration grows with each M2 ticket (SPEC §14), so a data
// directory made by an earlier build of it is at the same version and lacks the
// later parts. checkUnreleased looks for them when the database is opened, so
// that mismatch is one clear error and not a failure of whatever query first
// touches what is missing. When a release is tagged, take its version off this
// list: its migration is then fixed, and a later one is appended instead.
var unreleased = map[int][]schemaPart{
	2: {{"events", "client"}, {"events_by_key", ""}, {"baseline_places", ""}, {"key_learning", ""}, {"canaries", ""},
		{"incidents", ""}, {"detections", ""}, {"known_sources", ""}},
}

// checkUnreleased refuses a database that is at a version listed in unreleased
// but lacks part of what that version now creates. Only a version the
// migrations in ms reach is checked.
func (s *Store) checkUnreleased(ctx context.Context, ms []migration) error {
	for version := 1; version <= len(ms); version++ {
		var missing []string
		for _, p := range unreleased[version] {
			var n int
			var err error
			if p.column == "" {
				err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type IN ('table', 'index') AND name = ?`, p.table).Scan(&n)
			} else {
				err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, p.table, p.column).Scan(&n)
			}
			if err != nil {
				return fmt.Errorf("store: check the schema: %w", err)
			}
			if n == 0 {
				name := p.table
				if p.column != "" {
					name += "." + p.column
				}
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("store: %w: it lacks %s; rebuild the data directory (unreleased migration changed)", ErrStaleSchema, strings.Join(missing, ", "))
		}
	}
	return nil
}

// migrate applies every migration in ms that the database has not had yet.
// It runs forward only: a database from a newer kman is refused.
func (s *Store) migrate(ctx context.Context, now time.Time, ms []migration) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if v > len(ms) {
		return fmt.Errorf("store: %w: the database is at schema %d and this kman knows up to %d", ErrNewerSchema, v, len(ms))
	}
	if v == len(ms) {
		return nil
	}
	// A new database has nothing to keep. Any other is copied aside first,
	// so a failed or unwanted upgrade can be undone by restoring the copy.
	if v > 0 {
		if err := s.copyAside(ctx, len(ms)); err != nil {
			return err
		}
	}
	env := migrationEnv{now: now, rand: s.rand}
	for i := v; i < len(ms); i++ {
		if err := s.apply(ctx, i+1, ms[i], env); err != nil {
			return err
		}
	}
	return nil
}

// apply runs one migration and records its version in the same transaction.
func (s *Store) apply(ctx context.Context, version int, m migration, env migrationEnv) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migration %04d: %w", version, err)
	}
	defer tx.Rollback()
	if err := m(ctx, tx, env); err != nil {
		return fmt.Errorf("store: migration %04d: %w", version, err)
	}
	// PRAGMA takes no bound parameters; version is an int, never input.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("store: migration %04d: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %04d: %w", version, err)
	}
	return nil
}
