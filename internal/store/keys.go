package store

import (
	"database/sql"
	"time"
)

// A virtual key's kinds and statuses, as stored.
const (
	KeyStandard = "standard"
	KeyCanary   = "canary"

	KeyActive   = "active"
	KeyDisabled = "disabled"
	KeyRevoked  = "revoked"
)

// VirtualKey is a virtual key as stored. Its secret is not here: only a hash
// is kept, in KeySecret.
type VirtualKey struct {
	ID            string // the key id inside the token
	Name          string // "" for none; unique among keys not revoked
	CredentialID  string // "" for a canary
	Kind          string // defaults to KeyStandard
	Status        string // defaults to KeyActive
	Note          string
	TagsJSON      string
	PolicyJSON    string
	PolicyVersion int
	ExpiresAt     time.Time
	CreatedAt     time.Time
	DisabledAt    time.Time
	RevokedAt     time.Time
	Generation    int64 // set by the store
}

// A key secret's states, as stored.
const (
	SecretCurrent  = "current"
	SecretPrevious = "previous"
	SecretRetired  = "retired"
)

// KeySecret is the hash of one of a key's secrets.
type KeySecret struct {
	Hash       []byte
	State      string
	CreatedAt  time.Time
	ValidUntil time.Time // zero for no limit
	ForgetAt   time.Time // zero for no limit
}

const keyColumns = `key_id, name, credential_id, kind, status, note, tags_json, policy_json, policy_version,
	expires_at, created_at, disabled_at, revoked_at, generation`

// CreateKey inserts k with this transaction's generation, and secretHash as its
// current secret, and returns k as stored. The key id is the caller's, since
// the token holds it. A key id or a name already taken is ErrConflict.
func (tx *Tx) CreateKey(k VirtualKey, secretHash []byte) (VirtualKey, error) {
	gen, err := tx.Generation()
	if err != nil {
		return VirtualKey{}, err
	}
	if k.Kind == "" {
		k.Kind = KeyStandard
	}
	if k.Status == "" {
		k.Status = KeyActive
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO virtual_keys (`+keyColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+keyColumns,
		k.ID, nullString(k.Name), nullString(k.CredentialID), k.Kind, k.Status, k.Note, k.TagsJSON, k.PolicyJSON, k.PolicyVersion,
		nullMilli(k.ExpiresAt), toMilli(k.CreatedAt), nullMilli(k.DisabledAt), nullMilli(k.RevokedAt), gen)
	created, err := scanKey(row, "create key "+k.ID)
	if err != nil {
		return VirtualKey{}, err
	}
	_, err = tx.tx.ExecContext(tx.ctx, `INSERT INTO key_secrets (key_id, secret_hash, state, created_at) VALUES (?, ?, ?, ?)`,
		k.ID, secretHash, SecretCurrent, toMilli(k.CreatedAt))
	if err != nil {
		return VirtualKey{}, typed(err, "create key "+k.ID+" secret")
	}
	return created, nil
}

// Key returns the key with this key id, revoked or not, or ErrNotFound.
func (tx *Tx) Key(id string) (VirtualKey, error) {
	row := tx.tx.QueryRowContext(tx.ctx, `SELECT `+keyColumns+` FROM virtual_keys WHERE key_id = ?`, id)
	return scanKey(row, "key "+id)
}

// KeyByName returns the key not revoked with this name, or ErrNotFound. A
// revoked key's name may have passed to another key, so it is not found by it.
func (tx *Tx) KeyByName(name string) (VirtualKey, error) {
	row := tx.tx.QueryRowContext(tx.ctx, `SELECT `+keyColumns+` FROM virtual_keys WHERE name = ? AND status <> 'revoked'`, name)
	return scanKey(row, "key "+name)
}

// Keys returns every key, revoked or not, oldest first.
func (tx *Tx) Keys() ([]VirtualKey, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT `+keyColumns+` FROM virtual_keys ORDER BY created_at, key_id`)
	if err != nil {
		return nil, typed(err, "list keys")
	}
	defer rows.Close()
	var out []VirtualKey
	for rows.Next() {
		k, err := scanKey(rows, "list keys")
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list keys")
	}
	return out, nil
}

// UpdateKey writes what an operator can change on k, with this transaction's
// generation, and returns it as stored: not its id, kind, credential or
// creation time. A name already taken is ErrConflict.
func (tx *Tx) UpdateKey(k VirtualKey) (VirtualKey, error) {
	gen, err := tx.Generation()
	if err != nil {
		return VirtualKey{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE virtual_keys
		SET name = ?, status = ?, note = ?, tags_json = ?, policy_json = ?, policy_version = ?,
			expires_at = ?, disabled_at = ?, revoked_at = ?, generation = ?
		WHERE key_id = ? RETURNING `+keyColumns,
		nullString(k.Name), k.Status, k.Note, k.TagsJSON, k.PolicyJSON, k.PolicyVersion,
		nullMilli(k.ExpiresAt), nullMilli(k.DisabledAt), nullMilli(k.RevokedAt), gen, k.ID)
	return scanKey(row, "update key "+k.ID)
}

// KeySecrets returns the hashes of a key's secrets, newest first.
func (tx *Tx) KeySecrets(id string) ([]KeySecret, error) {
	rows, err := tx.tx.QueryContext(tx.ctx,
		`SELECT secret_hash, state, created_at, valid_until, forget_at FROM key_secrets WHERE key_id = ? ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, typed(err, "secrets of key "+id)
	}
	defer rows.Close()
	var out []KeySecret
	for rows.Next() {
		var s KeySecret
		var created int64
		var validUntil, forgetAt sql.NullInt64
		if err := rows.Scan(&s.Hash, &s.State, &created, &validUntil, &forgetAt); err != nil {
			return nil, typed(err, "secrets of key "+id)
		}
		s.CreatedAt, s.ValidUntil, s.ForgetAt = fromMilli(created), timeOf(validUntil), timeOf(forgetAt)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "secrets of key "+id)
	}
	return out, nil
}

func scanKey(row scanner, what string) (VirtualKey, error) {
	var k VirtualKey
	var name, credential sql.NullString
	var created int64
	var expires, disabled, revoked sql.NullInt64
	err := row.Scan(&k.ID, &name, &credential, &k.Kind, &k.Status, &k.Note, &k.TagsJSON, &k.PolicyJSON, &k.PolicyVersion,
		&expires, &created, &disabled, &revoked, &k.Generation)
	if err != nil {
		return VirtualKey{}, typed(err, what)
	}
	k.Name, k.CredentialID = name.String, credential.String
	k.CreatedAt, k.ExpiresAt, k.DisabledAt, k.RevokedAt = fromMilli(created), timeOf(expires), timeOf(disabled), timeOf(revoked)
	return k, nil
}

// timeOf is a stored nullable time, with NULL as the zero time.
func timeOf(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return fromMilli(n.Int64)
}

// RotateKeySecret gives a key a new secret, whose hash is newHash, at time at.
// The key stays; only its secrets move. The current secret becomes the
// previous one, which still works for grace, or, with no grace, is retired at
// once. A key holds at most two working secrets, so a previous secret still in
// its grace is retired now: a second rotation ends the older secret. A retired
// secret never authenticates, and its hash is kept for forgetAfter, for
// detection. The key's generation moves, so the change reaches the request
// path. A missing key is ErrNotFound, and a new hash the key already holds is
// ErrConflict; either leaves the secrets as they were once Do rolls back.
func (tx *Tx) RotateKeySecret(id string, newHash []byte, at time.Time, grace, forgetAfter time.Duration) error {
	gen, err := tx.Generation()
	if err != nil {
		return err
	}
	var got string
	err = tx.tx.QueryRowContext(tx.ctx, `UPDATE virtual_keys SET generation = ? WHERE key_id = ? RETURNING key_id`, gen, id).Scan(&got)
	if err != nil {
		return typed(err, "rotate key "+id)
	}
	retire := func(state string) error {
		_, err := tx.tx.ExecContext(tx.ctx, `UPDATE key_secrets
			SET state = 'retired', valid_until = MIN(COALESCE(valid_until, ?), ?), forget_at = MIN(COALESCE(valid_until, ?), ?) + ?
			WHERE key_id = ? AND state = ?`,
			toMilli(at), toMilli(at), toMilli(at), toMilli(at), forgetAfter.Milliseconds(), id, state)
		if err != nil {
			return typed(err, "retire secrets of key "+id)
		}
		return nil
	}
	if err := retire(SecretPrevious); err != nil {
		return err
	}
	if grace > 0 {
		_, err = tx.tx.ExecContext(tx.ctx, `UPDATE key_secrets SET state = 'previous', valid_until = ? WHERE key_id = ? AND state = 'current'`,
			toMilli(at.Add(grace)), id)
		if err != nil {
			return typed(err, "demote the secret of key "+id)
		}
	} else if err := retire(SecretCurrent); err != nil {
		return err
	}
	_, err = tx.tx.ExecContext(tx.ctx, `INSERT INTO key_secrets (key_id, secret_hash, state, created_at) VALUES (?, ?, 'current', ?)`,
		id, newHash, toMilli(at))
	if err != nil {
		return typed(err, "add the new secret of key "+id)
	}
	return nil
}

// SettleKeySecrets moves what time has decided, as of now: a previous secret
// whose grace has ended becomes a retired one, forgotten forgetAfter after the
// grace ended, and a retired secret past its forgetting is deleted. It returns
// when the next grace ends, or the zero time when none is running. It changes
// no key, so it takes no generation: the request path already stops a secret at
// the end of its grace.
func (tx *Tx) SettleKeySecrets(now time.Time, forgetAfter time.Duration) (time.Time, error) {
	_, err := tx.tx.ExecContext(tx.ctx, `UPDATE key_secrets SET state = 'retired', forget_at = valid_until + ?
		WHERE state = 'previous' AND valid_until <= ?`, forgetAfter.Milliseconds(), toMilli(now))
	if err != nil {
		return time.Time{}, typed(err, "retire secrets")
	}
	if _, err = tx.tx.ExecContext(tx.ctx, `DELETE FROM key_secrets WHERE state = 'retired' AND forget_at <= ?`, toMilli(now)); err != nil {
		return time.Time{}, typed(err, "forget secrets")
	}
	var next sql.NullInt64
	err = tx.tx.QueryRowContext(tx.ctx, `SELECT MIN(valid_until) FROM key_secrets WHERE state = 'previous'`).Scan(&next)
	if err != nil {
		return time.Time{}, typed(err, "next grace")
	}
	return timeOf(next), nil
}
