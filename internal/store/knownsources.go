package store

import (
	"fmt"
	"time"
)

// The kinds of known source (#88): a network, a country, an autonomous system or
// a client.
const (
	SourceCIDR    = "cidr"
	SourceCountry = "country"
	SourceASN     = "asn"
	SourceClient  = "client"
)

// KnownSource is a place an operator has said is legitimate: a labelled CIDR,
// country, ASN or client, for every key (Scope "") or for one (Scope is its key
// id). Use from one never counts as a new place to the location rules, and never
// silences a canary.
type KnownSource struct {
	ID        int64 // assigned by CreateKnownSource
	Scope     string
	Kind      string
	Value     string
	Label     string
	CreatedBy string
	CreatedAt time.Time
	// Generation is set by the store.
	Generation int64
}

const knownSourceColumns = `id, scope, kind, value, label, created_by, created_at, generation`

// CreateKnownSource adds ks with this transaction's generation. The same kind
// and value twice in one scope is ErrConflict.
func (tx *Tx) CreateKnownSource(ks KnownSource) (KnownSource, error) {
	gen, err := tx.Generation()
	if err != nil {
		return KnownSource{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO known_sources (scope, kind, value, label, created_by, created_at, generation)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING `+knownSourceColumns,
		ks.Scope, ks.Kind, ks.Value, ks.Label, ks.CreatedBy, toMilli(ks.CreatedAt), gen)
	return scanKnownSource(row, fmt.Sprintf("known source %s %s", ks.Kind, ks.Value))
}

// KnownSource returns the known source with this id, or ErrNotFound.
func (tx *Tx) KnownSource(id int64) (KnownSource, error) {
	row := tx.tx.QueryRowContext(tx.ctx, `SELECT `+knownSourceColumns+` FROM known_sources WHERE id = ?`, id)
	return scanKnownSource(row, fmt.Sprintf("known source %d", id))
}

// KnownSources returns every known source: the install-wide ones first, then
// each key's, oldest first.
func (tx *Tx) KnownSources() ([]KnownSource, error) {
	return tx.knownSources(`SELECT ` + knownSourceColumns + ` FROM known_sources ORDER BY scope <> '', scope, id`)
}

// KnownSourcesFor returns the known sources that apply to a key: the
// install-wide ones, then its own.
func (tx *Tx) KnownSourcesFor(keyID string) ([]KnownSource, error) {
	return tx.knownSources(`SELECT `+knownSourceColumns+` FROM known_sources WHERE scope = '' OR scope = ? ORDER BY scope <> '', id`, keyID)
}

// CountKnownSources returns how many known sources a scope has: "" for the
// install-wide ones.
func (tx *Tx) CountKnownSources(scope string) (int, error) {
	var n int
	if err := tx.tx.QueryRowContext(tx.ctx, `SELECT count(*) FROM known_sources WHERE scope = ?`, scope).Scan(&n); err != nil {
		return 0, typed(err, "count known sources")
	}
	return n, nil
}

// DeleteKnownSource removes a known source, or is ErrNotFound.
func (tx *Tx) DeleteKnownSource(id int64) error {
	var got int64
	err := tx.tx.QueryRowContext(tx.ctx, `DELETE FROM known_sources WHERE id = ? RETURNING id`, id).Scan(&got)
	if err != nil {
		return typed(err, fmt.Sprintf("delete known source %d", id))
	}
	return nil
}

func (tx *Tx) knownSources(query string, args ...any) ([]KnownSource, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, query, args...)
	if err != nil {
		return nil, typed(err, "list known sources")
	}
	defer rows.Close()
	var out []KnownSource
	for rows.Next() {
		ks, err := scanKnownSource(rows, "list known sources")
		if err != nil {
			return nil, err
		}
		out = append(out, ks)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list known sources")
	}
	return out, nil
}

func scanKnownSource(row scanner, what string) (KnownSource, error) {
	var ks KnownSource
	var created int64
	if err := row.Scan(&ks.ID, &ks.Scope, &ks.Kind, &ks.Value, &ks.Label, &ks.CreatedBy, &created, &ks.Generation); err != nil {
		return KnownSource{}, typed(err, what)
	}
	ks.CreatedAt = fromMilli(created)
	return ks, nil
}
