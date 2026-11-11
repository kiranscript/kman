package store

import (
	"database/sql"
	"time"
)

// Credential is a real secret for one upstream, as stored: sealed by the
// vault, so it holds nothing secret without the data key. Only live
// credentials are returned.
type Credential struct {
	ID         string // "cr_" plus 12 base62, from NewID: the sealing binds it
	UpstreamID string
	Name       string // unique among live credentials
	Ciphertext []byte
	Nonce      []byte
	DataKeyID  string
	Last4      string // the end of the secret, "" when too short to show any
	CreatedAt  time.Time
	RotatedAt  time.Time // zero until the secret is first replaced
	Generation int64     // set by the store
}

const credentialColumns = `id, upstream_id, name, ciphertext, nonce, dek_id, last4, created_at, rotated_at, generation`

// CreateCredential inserts c with this transaction's generation and returns
// it as stored. The id is the caller's, since sealing binds it. A live
// credential with the same name is ErrConflict.
func (tx *Tx) CreateCredential(c Credential) (Credential, error) {
	gen, err := tx.Generation()
	if err != nil {
		return Credential{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO credentials (`+credentialColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+credentialColumns,
		c.ID, c.UpstreamID, c.Name, c.Ciphertext, c.Nonce, c.DataKeyID, c.Last4, toMilli(c.CreatedAt), nullMilli(c.RotatedAt), gen)
	return scanCredential(row, "create credential "+c.Name)
}

// Credential returns the live credential with this id, or ErrNotFound.
func (tx *Tx) Credential(id string) (Credential, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+credentialColumns+` FROM credentials WHERE id = ? AND deleted_at IS NULL`, id)
	return scanCredential(row, "credential "+id)
}

// CredentialByName returns the live credential with this name, or ErrNotFound.
func (tx *Tx) CredentialByName(name string) (Credential, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+credentialColumns+` FROM credentials WHERE name = ? AND deleted_at IS NULL`, name)
	return scanCredential(row, "credential "+name)
}

// Credentials returns every live credential, by name.
func (tx *Tx) Credentials() ([]Credential, error) {
	rows, err := tx.tx.QueryContext(tx.ctx,
		`SELECT `+credentialColumns+` FROM credentials WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, typed(err, "list credentials")
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		c, err := scanCredential(rows, "list credentials")
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list credentials")
	}
	return out, nil
}

// UpdateCredential rewrites the name and the sealed secret of the live
// credential with c's id, stamping this transaction's generation. A replaced
// ciphertext is overwritten, never kept. A missing credential is ErrNotFound;
// a name another live credential holds is ErrConflict.
func (tx *Tx) UpdateCredential(c Credential) (Credential, error) {
	gen, err := tx.Generation()
	if err != nil {
		return Credential{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE credentials SET
		name = ?, ciphertext = ?, nonce = ?, dek_id = ?, last4 = ?, rotated_at = ?, generation = ?
		WHERE id = ? AND deleted_at IS NULL RETURNING `+credentialColumns,
		c.Name, c.Ciphertext, c.Nonce, c.DataKeyID, c.Last4, nullMilli(c.RotatedAt), gen, c.ID)
	return scanCredential(row, "update credential "+c.ID)
}

// DeleteCredential soft-deletes the live credential with this id at time at,
// freeing its name. A missing or already deleted credential is ErrNotFound.
func (tx *Tx) DeleteCredential(id string, at time.Time) error {
	gen, err := tx.Generation()
	if err != nil {
		return err
	}
	var got string
	err = tx.tx.QueryRowContext(tx.ctx,
		`UPDATE credentials SET deleted_at = ?, generation = ? WHERE id = ? AND deleted_at IS NULL RETURNING id`,
		toMilli(at), gen, id).Scan(&got)
	if err != nil {
		return typed(err, "delete credential "+id)
	}
	return nil
}

// KeysOnCredential returns the key ids of the virtual keys that depend on a
// credential: every key not revoked, since a disabled key can be enabled again.
func (tx *Tx) KeysOnCredential(id string) ([]string, error) {
	rows, err := tx.tx.QueryContext(tx.ctx,
		`SELECT key_id FROM virtual_keys WHERE credential_id = ? AND status <> 'revoked' ORDER BY key_id`, id)
	if err != nil {
		return nil, typed(err, "keys on credential "+id)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var keyID string
		if err := rows.Scan(&keyID); err != nil {
			return nil, typed(err, "keys on credential "+id)
		}
		out = append(out, keyID)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "keys on credential "+id)
	}
	return out, nil
}

func scanCredential(row scanner, what string) (Credential, error) {
	var c Credential
	var created int64
	var rotated sql.NullInt64
	err := row.Scan(&c.ID, &c.UpstreamID, &c.Name, &c.Ciphertext, &c.Nonce, &c.DataKeyID, &c.Last4, &created, &rotated, &c.Generation)
	if err != nil {
		return Credential{}, typed(err, what)
	}
	c.CreatedAt = fromMilli(created)
	if rotated.Valid {
		c.RotatedAt = fromMilli(rotated.Int64)
	}
	return c, nil
}

// nullMilli is t as stored, with the zero time as NULL.
func nullMilli(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: toMilli(t), Valid: true}
}
