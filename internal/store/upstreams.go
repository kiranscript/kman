package store

import (
	"database/sql"
	"time"
)

// Upstream is a provider API that kman forwards to. Only upstreams that are
// not deleted are returned.
type Upstream struct {
	ID                 string // "up_" plus 12 base62, assigned by CreateUpstream
	Name               string // unique among upstreams not deleted
	Preset             string // "" for a custom upstream
	PresetVersion      int
	Profile            string
	Host               string
	Port               int
	TLS                bool
	ServerName         string
	PathAllowlistJSON  string
	InjectUsage        bool
	PriceMultiplierPPM int64 // 1 000 000 is ×1
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Generation         int64 // set by the store
}

const upstreamColumns = `id, name, preset, preset_version, profile, host, port, tls, server_name,
	path_allowlist_json, inject_usage, price_multiplier_ppm, created_at, updated_at, generation`

// CreateUpstream inserts u with a new id and this transaction's generation,
// and returns it as stored. A live upstream with the same name is
// ErrConflict.
func (tx *Tx) CreateUpstream(u Upstream) (Upstream, error) {
	id, err := newID(tx.rand, "up_")
	if err != nil {
		return Upstream{}, err
	}
	gen, err := tx.Generation()
	if err != nil {
		return Upstream{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO upstreams (`+upstreamColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING `+upstreamColumns,
		id, u.Name, u.Preset, u.PresetVersion, u.Profile, u.Host, u.Port, u.TLS, u.ServerName,
		u.PathAllowlistJSON, u.InjectUsage, u.PriceMultiplierPPM, toMilli(u.CreatedAt), toMilli(u.UpdatedAt), gen)
	return scanUpstream(row, "create upstream "+u.Name)
}

// Upstream returns the live upstream with this id, or ErrNotFound.
func (tx *Tx) Upstream(id string) (Upstream, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+upstreamColumns+` FROM upstreams WHERE id = ? AND deleted_at IS NULL`, id)
	return scanUpstream(row, "upstream "+id)
}

// UpstreamByName returns the live upstream with this name, or ErrNotFound.
func (tx *Tx) UpstreamByName(name string) (Upstream, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+upstreamColumns+` FROM upstreams WHERE name = ? AND deleted_at IS NULL`, name)
	return scanUpstream(row, "upstream "+name)
}

// DeleteUpstream soft-deletes the live upstream with this id at time at,
// freeing its name. A missing or already deleted upstream is ErrNotFound.
func (tx *Tx) DeleteUpstream(id string, at time.Time) error {
	gen, err := tx.Generation()
	if err != nil {
		return err
	}
	var got string
	err = tx.tx.QueryRowContext(tx.ctx,
		`UPDATE upstreams SET deleted_at = ?, updated_at = ?, generation = ? WHERE id = ? AND deleted_at IS NULL RETURNING id`,
		toMilli(at), toMilli(at), gen, id).Scan(&got)
	if err != nil {
		return typed(err, "delete upstream "+id)
	}
	return nil
}

func scanUpstream(row *sql.Row, what string) (Upstream, error) {
	var u Upstream
	var created, updated int64
	err := row.Scan(&u.ID, &u.Name, &u.Preset, &u.PresetVersion, &u.Profile, &u.Host, &u.Port, &u.TLS,
		&u.ServerName, &u.PathAllowlistJSON, &u.InjectUsage, &u.PriceMultiplierPPM, &created, &updated, &u.Generation)
	if err != nil {
		return Upstream{}, typed(err, what)
	}
	u.CreatedAt, u.UpdatedAt = fromMilli(created), fromMilli(updated)
	return u, nil
}
