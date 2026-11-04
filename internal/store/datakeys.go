package store

import "time"

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
