package store

import (
	"database/sql"
	"time"
)

// AdminToken is a named admin token. Only the SHA-256 hash of its secret is
// stored (ADR 0001); admin tokens have no key id, so they are looked up by
// hash.
type AdminToken struct {
	Name       string // such as "owner"; the actor of what it changes
	Hash       []byte // token.Hash of the secret, 32 bytes
	CreatedAt  time.Time
	LastUsedAt time.Time // zero for never
	RevokedAt  time.Time // zero for not revoked
	Generation int64     // set by the store
}

const adminTokenColumns = `name, token_hash, created_at, last_used_at, revoked_at, generation`

// CreateAdminToken inserts t with this transaction's generation and returns
// it as stored. A name or hash already taken is ErrConflict.
func (tx *Tx) CreateAdminToken(t AdminToken) (AdminToken, error) {
	gen, err := tx.Generation()
	if err != nil {
		return AdminToken{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO admin_tokens (name, token_hash, created_at, generation)
		VALUES (?, ?, ?, ?) RETURNING `+adminTokenColumns,
		t.Name, t.Hash, toMilli(t.CreatedAt), gen)
	return scanAdminToken(row, "create admin token "+t.Name)
}

// AdminTokenByHash returns the admin token whose secret hashes to hash,
// revoked or not, or ErrNotFound.
func (tx *Tx) AdminTokenByHash(hash []byte) (AdminToken, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+adminTokenColumns+` FROM admin_tokens WHERE token_hash = ?`, hash)
	return scanAdminToken(row, "admin token by hash")
}

// AdminTokenByName returns the admin token with this name, revoked or not,
// or ErrNotFound.
func (tx *Tx) AdminTokenByName(name string) (AdminToken, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+adminTokenColumns+` FROM admin_tokens WHERE name = ?`, name)
	return scanAdminToken(row, "admin token "+name)
}

// AdminTokens returns every admin token, revoked or not, by name.
func (tx *Tx) AdminTokens() ([]AdminToken, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT `+adminTokenColumns+` FROM admin_tokens ORDER BY name`)
	if err != nil {
		return nil, typed(err, "list admin tokens")
	}
	defer rows.Close()
	var out []AdminToken
	for rows.Next() {
		t, err := scanAdminToken(rows, "list admin tokens")
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list admin tokens")
	}
	return out, nil
}

// RotateAdminToken replaces the secret hash of the live admin token name,
// which keeps its name and so its actor. A missing or revoked token is
// ErrNotFound.
func (tx *Tx) RotateAdminToken(name string, hash []byte) (AdminToken, error) {
	gen, err := tx.Generation()
	if err != nil {
		return AdminToken{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE admin_tokens SET token_hash = ?, generation = ?
		WHERE name = ? AND revoked_at IS NULL RETURNING `+adminTokenColumns, hash, gen, name)
	return scanAdminToken(row, "rotate admin token "+name)
}

// RevokeAdminToken revokes the live admin token name at time at. The row is
// kept, so its name stays taken and its history readable. A missing or
// revoked token is ErrNotFound.
func (tx *Tx) RevokeAdminToken(name string, at time.Time) (AdminToken, error) {
	gen, err := tx.Generation()
	if err != nil {
		return AdminToken{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE admin_tokens SET revoked_at = ?, generation = ?
		WHERE name = ? AND revoked_at IS NULL RETURNING `+adminTokenColumns, toMilli(at), gen, name)
	return scanAdminToken(row, "revoke admin token "+name)
}

// TouchAdminToken records that the token name was used at time at. Use is
// not a change to the install, so it takes no generation. A missing token is
// ErrNotFound.
func (tx *Tx) TouchAdminToken(name string, at time.Time) error {
	var got string
	err := tx.tx.QueryRowContext(tx.ctx,
		`UPDATE admin_tokens SET last_used_at = ? WHERE name = ? RETURNING name`, toMilli(at), name).Scan(&got)
	if err != nil {
		return typed(err, "touch admin token "+name)
	}
	return nil
}

// scanner is a *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanAdminToken(row scanner, what string) (AdminToken, error) {
	var t AdminToken
	var created int64
	var used, revoked sql.NullInt64
	if err := row.Scan(&t.Name, &t.Hash, &created, &used, &revoked, &t.Generation); err != nil {
		return AdminToken{}, typed(err, what)
	}
	t.CreatedAt = fromMilli(created)
	if used.Valid {
		t.LastUsedAt = fromMilli(used.Int64)
	}
	if revoked.Valid {
		t.RevokedAt = fromMilli(revoked.Int64)
	}
	return t, nil
}
