package store

import "errors"

var (
	// ErrNotFound is returned when the row asked for does not exist, or has
	// been deleted.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned when a write would break a uniqueness rule,
	// such as two live upstreams with the same name.
	ErrConflict = errors.New("conflict")
	// ErrNewerSchema is returned by Open when the database was migrated by a
	// newer kman. Migrations run forward only; downgrading means restoring
	// the copy taken before the upgrade.
	ErrNewerSchema = errors.New("database schema is newer than this kman")
)
