package store

import (
	"fmt"
	"time"
)

// DataKey is the data key as stored: wrapped under the master key, so it
// holds nothing secret without it. vault.WrappedKey is the same thing on the
// vault's side.
type DataKey struct {
	ID             string // "dek_" plus 12 base62, from NewID
	Wrapped        []byte
	Nonce          []byte
	KEKFingerprint string // names the master key that wraps it
	CreatedAt      time.Time
}

// CreateDataKey stores k as the current data key. The id is the caller's,
// since the wrapping binds it. A second current data key is ErrConflict.
func (tx *Tx) CreateDataKey(k DataKey) error {
	_, err := tx.tx.ExecContext(tx.ctx,
		`INSERT INTO data_keys (dek_id, wrapped, nonce, kek_fingerprint, created_at) VALUES (?, ?, ?, ?, ?)`,
		k.ID, k.Wrapped, k.Nonce, k.KEKFingerprint, toMilli(k.CreatedAt))
	if err != nil {
		return typed(err, "create data key "+k.ID)
	}
	return nil
}

// CurrentDataKey returns the data key that is not retired, or ErrNotFound
// for a store that has none.
func (tx *Tx) CurrentDataKey() (DataKey, error) {
	var k DataKey
	var created int64
	err := tx.tx.QueryRowContext(tx.ctx,
		`SELECT dek_id, wrapped, nonce, kek_fingerprint, created_at FROM data_keys WHERE retired_at IS NULL`).
		Scan(&k.ID, &k.Wrapped, &k.Nonce, &k.KEKFingerprint, &created)
	if err != nil {
		return DataKey{}, typed(err, "current data key")
	}
	k.CreatedAt = fromMilli(created)
	return k, nil
}

// SecretKind says which table a sealed secret lives in.
type SecretKind int

const (
	SecretCredential SecretKind = iota + 1
	SecretAlertChannel
)

// SealedSecret is one secret sealed under the data key, whichever table holds
// it. Its id is what the sealing binds, so it stays the same under a new key.
type SealedSecret struct {
	Kind       SecretKind
	ID         string
	Ciphertext []byte
	Nonce      []byte
	DataKeyID  string
}

// SealedSecrets returns every live sealed secret: what rekey must carry over.
func (tx *Tx) SealedSecrets() ([]SealedSecret, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `
		SELECT 1, id, ciphertext, nonce, dek_id FROM credentials WHERE deleted_at IS NULL
		UNION ALL
		SELECT 2, id, config_ciphertext, nonce, dek_id FROM alert_channels WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, typed(err, "list sealed secrets")
	}
	defer rows.Close()
	var out []SealedSecret
	for rows.Next() {
		var s SealedSecret
		if err := rows.Scan(&s.Kind, &s.ID, &s.Ciphertext, &s.Nonce, &s.DataKeyID); err != nil {
			return nil, typed(err, "list sealed secrets")
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list sealed secrets")
	}
	return out, nil
}

// ReplaceDataKey makes k the data key in place of the current one, oldID, and
// stores secrets, already sealed under k, over the rows they came from. The
// row of oldID is deleted, so nothing it wrapped remains, and rows that are
// themselves deleted lose their ciphertext, as it could never be opened again.
// A live secret left out of secrets fails the call, since its row would still
// need the old key. Run it in the transaction that does the rest of the rekey,
// and roll back on any error.
func (tx *Tx) ReplaceDataKey(oldID string, k DataKey, secrets []SealedSecret, at time.Time) error {
	// The old key is retired first: data_keys_one_current allows one current
	// key, and the foreign keys need both rows until every secret has moved.
	var got string
	err := tx.tx.QueryRowContext(tx.ctx,
		`UPDATE data_keys SET retired_at = ? WHERE dek_id = ? AND retired_at IS NULL RETURNING dek_id`,
		toMilli(at), oldID).Scan(&got)
	if err != nil {
		return typed(err, "retire data key "+oldID)
	}
	if err := tx.CreateDataKey(k); err != nil {
		return err
	}
	gen, err := tx.Generation()
	if err != nil {
		return err
	}
	for _, s := range secrets {
		var query string
		args := []any{s.Ciphertext, s.Nonce, k.ID, gen}
		switch s.Kind {
		case SecretCredential:
			query = `UPDATE credentials SET ciphertext = ?, nonce = ?, dek_id = ?, generation = ?
				WHERE id = ? AND deleted_at IS NULL RETURNING id`
		case SecretAlertChannel:
			query = `UPDATE alert_channels SET config_ciphertext = ?, nonce = ?, dek_id = ?, generation = ?, updated_at = ?
				WHERE id = ? AND deleted_at IS NULL RETURNING id`
			args = append(args, toMilli(at))
		default:
			return fmt.Errorf("store: reseal %s: unknown kind %d", s.ID, s.Kind)
		}
		if err := tx.tx.QueryRowContext(tx.ctx, query, append(args, s.ID)...).Scan(&got); err != nil {
			return typed(err, "reseal "+s.ID)
		}
	}
	for _, q := range []string{
		`UPDATE credentials SET ciphertext = x'', nonce = x'', dek_id = ?, generation = ? WHERE deleted_at IS NOT NULL`,
		`UPDATE alert_channels SET config_ciphertext = x'', nonce = x'', dek_id = ?, generation = ? WHERE deleted_at IS NOT NULL`,
	} {
		if _, err := tx.tx.ExecContext(tx.ctx, q, k.ID, gen); err != nil {
			return typed(err, "clear deleted secrets")
		}
	}
	// A foreign key fails this if any row still needs the old key.
	if _, err := tx.tx.ExecContext(tx.ctx, `DELETE FROM data_keys WHERE dek_id = ?`, oldID); err != nil {
		return typed(err, "delete data key "+oldID)
	}
	return nil
}
