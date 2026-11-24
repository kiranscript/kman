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
// an ASN, a network (IPv4 /24, IPv6 /64), or a client (a User-Agent reduced to a
// product and its major version, #86). count is requests kman forwarded
// and blocked those it refused, so a refusal still shows where a key turned up.
// A value of ” is the row that counts a key's places of one kind past the
// first 1,000. It has no foreign key, like events, and is kept forever.
//
// events gains client, the same reduction, which is NULL for an event stored
// before kman recorded it. Migration 0001 is a tagged release's and never
// changes, so the column is added here.
const schema0002 = `
ALTER TABLE events ADD COLUMN client TEXT;

CREATE TABLE baseline_places (
	key_id     TEXT    NOT NULL,
	kind       TEXT    NOT NULL CHECK (kind IN ('country', 'asn', 'network', 'client')),
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
// The events hold no AS organisation, so those labels come with the next use,
// and no client, so a key's clients are learnt from its next requests: an event
// from before has no client, which is not the client none (a request that sent
// no User-Agent).
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
