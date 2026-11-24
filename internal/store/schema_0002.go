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
//
// key_learning is when each key's learning period began (#89): the time of its
// first forwarded request or realtime session, which is not the first event, so
// a key that was only refused has not started to be learned. It is kept forever
// and holds one row per key.
//
// canaries holds what a canary key has beyond its virtual_keys row, whose kind
// is canary and which has no credential (#87): where it was planted, its ignore
// list (a JSON array of addresses and CIDR prefixes), when it was armed (made,
// or last reset), and what its use has done. fired_at is the first use since it
// was armed, NULL while it is armed; a reset clears it and nothing else of the
// history, so last_fired_at, fires and what the last use looked like stay. The
// last_* columns are the address, country, ASN, its organisation and the client
// of the last use: an address is personal data, so the admin API gives it out
// only with expose_client_addresses on (ADR 0010).
//
// incidents are the case kman opens for a key when a rule fires (#88). A key has
// at most one unresolved incident, which the partial unique index enforces.
// severity is the highest of its detections and alerted_severity the severity
// the last alert queued for it named; rules_json is the rules that have fired;
// detection_count counts every detection raised, repeats included, while
// detections holds a row for each only up to a bound. state is open,
// acknowledged or resolved, and a resolved incident has a resolution:
// confirmed, false_positive or expired. would_disable_at and would_block_count
// are the dry-run results (auto-disable, a later ticket). Ids are plain
// integers, which an operator types, and never reused.
//
// known_sources are the places an operator has said are legitimate (#88): a
// CIDR, a country, an ASN or a client, for every key (scope ”) or one key
// (scope is its key id). A canary ignores them; only its own ignore list
// silences one (ADR 0013).
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

CREATE TABLE key_learning (
	key_id     TEXT    PRIMARY KEY,
	started_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE TABLE canaries (
	key_id        TEXT    PRIMARY KEY REFERENCES virtual_keys (key_id),
	planted       TEXT    NOT NULL,
	ignore_json   TEXT    NOT NULL DEFAULT '[]',
	armed_at      INTEGER NOT NULL,
	fired_at      INTEGER,
	last_fired_at INTEGER,
	fires         INTEGER NOT NULL DEFAULT 0,
	last_ip       TEXT,
	last_country  TEXT,
	last_asn      INTEGER,
	last_as_org   TEXT,
	last_client   TEXT
) STRICT;

CREATE TABLE incidents (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	key_id           TEXT    NOT NULL REFERENCES virtual_keys (key_id),
	state            TEXT    NOT NULL CHECK (state IN ('open', 'acknowledged', 'resolved')),
	severity         TEXT    NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
	alerted_severity TEXT    CHECK (alerted_severity IN ('low', 'medium', 'high', 'critical')),
	rules_json       TEXT    NOT NULL DEFAULT '[]',
	detection_count  INTEGER NOT NULL DEFAULT 0,
	opened_at        INTEGER NOT NULL,
	last_fired_at    INTEGER NOT NULL,
	acknowledged_at  INTEGER,
	acknowledged_by  TEXT,
	resolved_at      INTEGER,
	resolved_by      TEXT,
	resolution       TEXT    CHECK (resolution IN ('confirmed', 'false_positive', 'expired')),
	would_disable_at INTEGER,
	would_block_count INTEGER NOT NULL DEFAULT 0,
	generation       INTEGER NOT NULL,
	CHECK ((state = 'resolved') = (resolution IS NOT NULL))
) STRICT;

CREATE UNIQUE INDEX incidents_one_unresolved ON incidents (key_id) WHERE state <> 'resolved';
CREATE INDEX incidents_by_key ON incidents (key_id, id);
CREATE INDEX incidents_quiet ON incidents (last_fired_at) WHERE state = 'open';

CREATE TABLE detections (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	incident_id   INTEGER NOT NULL REFERENCES incidents (id),
	key_id        TEXT    NOT NULL REFERENCES virtual_keys (key_id),
	ts            INTEGER NOT NULL,
	rule          TEXT    NOT NULL,
	severity      TEXT    NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
	evidence_json TEXT    NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX detections_by_incident ON detections (incident_id, id);

CREATE TABLE known_sources (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	scope      TEXT    NOT NULL DEFAULT '',
	kind       TEXT    NOT NULL CHECK (kind IN ('cidr', 'country', 'asn', 'client')),
	value      TEXT    NOT NULL,
	label      TEXT    NOT NULL,
	created_by TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	generation INTEGER NOT NULL,
	UNIQUE (scope, kind, value)
) STRICT;
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
	learning := map[string]int64{} // each key's first forwarded request, which is when its learning period began
	var learners []string
	for i := 0; rows.Next(); i++ {
		var r EventRow
		var ts int64
		if err := rows.Scan(&ts, &r.KeyID, &r.Kind, &r.ClientIP, &r.Country, &r.ASN); err != nil {
			rows.Close()
			return err
		}
		r.Time = fromMilli(ts)
		tally.add(i, r)
		if _, ok := learning[r.KeyID]; !ok && r.Kind != "blocked" {
			learning[r.KeyID] = ts
			learners = append(learners, r.KeyID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, key := range learners {
		if _, err := tx.ExecContext(ctx, `INSERT INTO key_learning (key_id, started_at) VALUES (?, ?)`, key, learning[key]); err != nil {
			return err
		}
	}
	_, err = tally.write(ctx, tx)
	return err
}
