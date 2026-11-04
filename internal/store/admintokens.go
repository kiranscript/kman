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

func scanAdminToken(row *sql.Row, what string) (AdminToken, error) {
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
