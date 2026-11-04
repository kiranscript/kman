package store

import "time"

// Setting is one row of the settings table: what an operator changes while
// kman runs, through the admin API. The admin module owns the keys, their
// defaults and the shape of each value.
type Setting struct {
	Key        string
	ValueJSON  string
	UpdatedAt  time.Time
	Generation int64 // set by the store
}

// PutSetting sets s.Key to s.ValueJSON with this transaction's generation,
// and returns the row as stored.
func (tx *Tx) PutSetting(s Setting) (Setting, error) {
	gen, err := tx.Generation()
	if err != nil {
		return Setting{}, err
	}
	var out Setting
	var updated int64
	err = tx.tx.QueryRowContext(tx.ctx, `INSERT INTO settings (key, value_json, updated_at, generation) VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at, generation = excluded.generation
		RETURNING key, value_json, updated_at, generation`,
		s.Key, s.ValueJSON, toMilli(s.UpdatedAt), gen).Scan(&out.Key, &out.ValueJSON, &updated, &out.Generation)
	if err != nil {
		return Setting{}, typed(err, "put setting "+s.Key)
	}
	out.UpdatedAt = fromMilli(updated)
	return out, nil
}

// Settings returns every setting that has been set, by key.
func (tx *Tx) Settings() ([]Setting, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT key, value_json, updated_at, generation FROM settings ORDER BY key`)
	if err != nil {
		return nil, typed(err, "list settings")
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var s Setting
		var updated int64
		if err := rows.Scan(&s.Key, &s.ValueJSON, &updated, &s.Generation); err != nil {
			return nil, typed(err, "list settings")
		}
		s.UpdatedAt = fromMilli(updated)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list settings")
	}
	return out, nil
}
