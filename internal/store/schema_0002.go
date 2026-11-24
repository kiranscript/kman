package store

import (
	"context"
	"database/sql"
)

// schema0002 is the M2 Detect schema (v0.2), from the schema ticket (#25), in
// the order #83 builds it. Until v0.2.0 is tagged, each M2 ticket adds its
// tables here; after a tag, a later ticket appends a migration of its own.
//
// baseline_places is every place each key has been used from (#84): a country,
// an ASN, or a network (IPv4 /24, IPv6 /64). count is requests kman forwarded
// and blocked those it refused, so a refusal still shows where a key turned up.
// A value of ” is the row that counts a key's places of one kind past the
// first 1,000. It has no foreign key, like events, and is kept forever.
const schema0002 = `
CREATE TABLE baseline_places (
	key_id     TEXT    NOT NULL,
	kind       TEXT    NOT NULL CHECK (kind IN ('country', 'asn', 'network')),
	value      TEXT    NOT NULL,
	label      TEXT,
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	count      INTEGER NOT NULL DEFAULT 0,
	blocked    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (key_id, kind, value)
) STRICT, WITHOUT ROWID;
`

// migrate0002 creates the M2 tables and fills baseline_places from the events
// already stored, oldest first, so a key's earliest places are the ones kept.
// The events hold no AS organisation, so those labels come with the next use.
func migrate0002(ctx context.Context, tx *sql.Tx, _ migrationEnv) error {
	if _, err := tx.ExecContext(ctx, schema0002); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT ts, key_id, kind, COALESCE(client_ip, ''), COALESCE(country, ''), COALESCE(asn, 0)
		FROM events WHERE key_id IS NOT NULL ORDER BY ts, id`)
	if err != nil {
		return err
	}
	tally := newPlaceTally()
	for rows.Next() {
		var r EventRow
		var ts int64
		if err := rows.Scan(&ts, &r.KeyID, &r.Kind, &r.ClientIP, &r.Country, &r.ASN); err != nil {
			rows.Close()
			return err
		}
		r.Time = fromMilli(ts)
		tally.add(r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return tally.write(ctx, tx)
}
