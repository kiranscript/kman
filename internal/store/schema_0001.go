package store

import (
	"context"
	"database/sql"
)

// schema0001 is the M1 core schema (v0.1), from the schema ticket (#25).
//
// Conventions, which every later migration keeps:
//   - every table is STRICT;
//   - timestamps are INTEGER Unix milliseconds in UTC, computed in Go;
//   - money is INTEGER microdollars; token prices are microdollars per million
//     tokens;
//   - ids are random prefixed TEXT (up_, cr_, ch_, ...), except virtual keys
//     (their key id) and incidents (INTEGER, from 0002);
//   - booleans are INTEGER 0 or 1;
//   - JSON is TEXT, validated by the module that owns it;
//   - generation is the meta.generation value of the admin transaction that
//     last changed the row;
//   - configuration rows (upstreams, credentials, alert channels) are
//     soft-deleted with deleted_at, and names are unique only among rows that
//     are not deleted or revoked.
//
// The spend flush interval (5 s, SPEC Consistency fixes 1) belongs to meter;
// spend_daily only ever receives increments, whatever the interval.
const schema0001 = `
CREATE TABLE meta (
	id                 INTEGER PRIMARY KEY CHECK (id = 1),
	install_id         TEXT    NOT NULL,
	created_at         INTEGER NOT NULL,
	generation         INTEGER NOT NULL DEFAULT 0,
	audit_prune_before INTEGER
) STRICT;

CREATE TABLE data_keys (
	dek_id          TEXT    PRIMARY KEY,
	wrapped         BLOB    NOT NULL,
	nonce           BLOB    NOT NULL,
	kek_fingerprint TEXT    NOT NULL,
	created_at      INTEGER NOT NULL,
	retired_at      INTEGER
) STRICT;
-- At most one data key is current: rekey retires the old one first.
CREATE UNIQUE INDEX data_keys_one_current ON data_keys ((retired_at IS NULL)) WHERE retired_at IS NULL;

CREATE TABLE settings (
	key        TEXT    PRIMARY KEY,
	value_json TEXT    NOT NULL,
	updated_at INTEGER NOT NULL,
	generation INTEGER NOT NULL
) STRICT;

CREATE TABLE upstreams (
	id                   TEXT    PRIMARY KEY,
	name                 TEXT    NOT NULL,
	preset               TEXT    NOT NULL DEFAULT '',
	preset_version       INTEGER NOT NULL DEFAULT 0,
	profile              TEXT    NOT NULL,
	host                 TEXT    NOT NULL,
	port                 INTEGER NOT NULL,
	tls                  INTEGER NOT NULL CHECK (tls IN (0, 1)),
	server_name          TEXT    NOT NULL DEFAULT '',
	path_allowlist_json  TEXT    NOT NULL DEFAULT '[]',
	inject_usage         INTEGER NOT NULL DEFAULT 0 CHECK (inject_usage IN (0, 1)),
	price_multiplier_ppm INTEGER NOT NULL DEFAULT 1000000,
	created_at           INTEGER NOT NULL,
	updated_at           INTEGER NOT NULL,
	deleted_at           INTEGER,
	generation           INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX upstreams_live_name ON upstreams (name) WHERE deleted_at IS NULL;

CREATE TABLE credentials (
	id          TEXT    PRIMARY KEY,
	upstream_id TEXT    NOT NULL REFERENCES upstreams (id),
	name        TEXT    NOT NULL,
	auth_json   TEXT    NOT NULL,
	ciphertext  BLOB    NOT NULL,
	nonce       BLOB    NOT NULL,
	dek_id      TEXT    NOT NULL REFERENCES data_keys (dek_id),
	last4       TEXT    NOT NULL,
	created_at  INTEGER NOT NULL,
	rotated_at  INTEGER,
	deleted_at  INTEGER,
	generation  INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX credentials_live_name ON credentials (name) WHERE deleted_at IS NULL;
CREATE INDEX credentials_upstream ON credentials (upstream_id);

CREATE TABLE virtual_keys (
	key_id                    TEXT    PRIMARY KEY,
	name                      TEXT    NOT NULL,
	credential_id             TEXT    REFERENCES credentials (id),
	kind                      TEXT    NOT NULL CHECK (kind IN ('standard', 'canary')),
	status                    TEXT    NOT NULL CHECK (status IN ('active', 'disabled', 'revoked')),
	note                      TEXT    NOT NULL DEFAULT '',
	tags_json                 TEXT    NOT NULL DEFAULT '[]',
	policy_json               TEXT    NOT NULL DEFAULT '{}',
	policy_version            INTEGER NOT NULL,
	expires_at                INTEGER,
	created_at                INTEGER NOT NULL,
	disabled_at               INTEGER,
	revoked_at                INTEGER,
	auto_disable_paused_until INTEGER,
	generation                INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX virtual_keys_live_name ON virtual_keys (name) WHERE status <> 'revoked';
CREATE INDEX virtual_keys_credential ON virtual_keys (credential_id);

CREATE TABLE key_secrets (
	key_id      TEXT    NOT NULL REFERENCES virtual_keys (key_id),
	secret_hash BLOB    NOT NULL,
	state       TEXT    NOT NULL CHECK (state IN ('current', 'previous', 'retired')),
	created_at  INTEGER NOT NULL,
	valid_until INTEGER,
	forget_at   INTEGER,
	PRIMARY KEY (key_id, secret_hash)
) STRICT;
CREATE UNIQUE INDEX key_secrets_one_current ON key_secrets (key_id) WHERE state = 'current';
CREATE UNIQUE INDEX key_secrets_one_previous ON key_secrets (key_id) WHERE state = 'previous';

CREATE TABLE admin_tokens (
	name         TEXT    PRIMARY KEY,
	token_hash   BLOB    NOT NULL UNIQUE,
	created_at   INTEGER NOT NULL,
	last_used_at INTEGER,
	revoked_at   INTEGER,
	generation   INTEGER NOT NULL
) STRICT;

CREATE TABLE price_overrides (
	scope      TEXT    NOT NULL,
	model      TEXT    NOT NULL,
	input      INTEGER NOT NULL,
	output     INTEGER NOT NULL,
	max_output INTEGER NOT NULL,
	extra_json TEXT    NOT NULL DEFAULT '{}',
	actor      TEXT    NOT NULL,
	updated_at INTEGER NOT NULL,
	generation INTEGER NOT NULL,
	PRIMARY KEY (scope, model)
) STRICT;

CREATE TABLE spend_daily (
	key_id             TEXT    NOT NULL REFERENCES virtual_keys (key_id),
	day                TEXT    NOT NULL,
	cost_micro         INTEGER NOT NULL DEFAULT 0,
	estimated_micro    INTEGER NOT NULL DEFAULT 0,
	requests           INTEGER NOT NULL DEFAULT 0,
	unmetered_requests INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (key_id, day)
) STRICT;

CREATE TABLE budget_alerts (
	key_id    TEXT    NOT NULL REFERENCES virtual_keys (key_id),
	period    TEXT    NOT NULL,
	threshold INTEGER NOT NULL,
	sent_at   INTEGER NOT NULL,
	PRIMARY KEY (key_id, period, threshold)
) STRICT;

CREATE TABLE events (
	id                 INTEGER PRIMARY KEY,
	ts                 INTEGER NOT NULL,
	key_id             TEXT,
	kind               TEXT    NOT NULL CHECK (kind IN ('request', 'blocked', 'session')),
	client_ip          TEXT,
	country            TEXT,
	asn                INTEGER,
	upstream_id        TEXT,
	model              TEXT,
	operation          TEXT,
	status             INTEGER,
	blocked_code       TEXT,
	latency_ms         INTEGER,
	req_bytes          INTEGER,
	resp_bytes         INTEGER,
	tokens_in          INTEGER,
	tokens_out         INTEGER,
	tokens_cached      INTEGER,
	tokens_cache_write INTEGER,
	tokens_audio_in    INTEGER,
	tokens_audio_out   INTEGER,
	cost_micro         INTEGER,
	cost_estimated     INTEGER NOT NULL DEFAULT 0 CHECK (cost_estimated IN (0, 1)),
	incident_id        INTEGER
) STRICT;
CREATE INDEX events_key_ts ON events (key_id, ts);
CREATE INDEX events_ts ON events (ts);

CREATE TABLE usage_hourly (
	key_id     TEXT    NOT NULL,
	hour       INTEGER NOT NULL,
	requests   INTEGER NOT NULL DEFAULT 0,
	blocked    INTEGER NOT NULL DEFAULT 0,
	errors_4xx INTEGER NOT NULL DEFAULT 0,
	errors_5xx INTEGER NOT NULL DEFAULT 0,
	tokens_in  INTEGER NOT NULL DEFAULT 0,
	tokens_out INTEGER NOT NULL DEFAULT 0,
	cost_micro INTEGER NOT NULL DEFAULT 0,
	unmetered  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (key_id, hour)
) STRICT;

CREATE TABLE audit_log (
	id          INTEGER PRIMARY KEY,
	ts          INTEGER NOT NULL,
	actor       TEXT    NOT NULL,
	action      TEXT    NOT NULL,
	target_kind TEXT    NOT NULL,
	target_id   TEXT    NOT NULL,
	incident_id INTEGER,
	before_json TEXT,
	after_json  TEXT,
	client_ip   TEXT,
	request_id  TEXT
) STRICT;
CREATE INDEX audit_log_target ON audit_log (target_kind, target_id, ts);

-- The audit log is append-only. The one exception is a guarded prune: a
-- transaction sets meta.audit_prune_before, deletes the rows older than it,
-- clears it, and appends an entry recording the prune.
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN
	SELECT RAISE(ABORT, 'audit_log is append-only');
END;
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
WHEN (SELECT audit_prune_before FROM meta WHERE id = 1) IS NULL
	OR OLD.ts >= (SELECT audit_prune_before FROM meta WHERE id = 1)
BEGIN
	SELECT RAISE(ABORT, 'audit_log is append-only');
END;

CREATE TABLE alert_channels (
	id                TEXT    PRIMARY KEY,
	name              TEXT    NOT NULL,
	kind              TEXT    NOT NULL,
	config_ciphertext BLOB    NOT NULL,
	nonce             BLOB    NOT NULL,
	dek_id            TEXT    NOT NULL REFERENCES data_keys (dek_id),
	display_host      TEXT    NOT NULL,
	min_severity      TEXT    NOT NULL,
	lifecycle_alerts  INTEGER NOT NULL DEFAULT 1 CHECK (lifecycle_alerts IN (0, 1)),
	created_at        INTEGER NOT NULL,
	updated_at        INTEGER NOT NULL,
	deleted_at        INTEGER,
	generation        INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX alert_channels_live_name ON alert_channels (name) WHERE deleted_at IS NULL;

CREATE TABLE alert_outbox (
	id              INTEGER PRIMARY KEY,
	channel_id      TEXT    NOT NULL REFERENCES alert_channels (id),
	kind            TEXT    NOT NULL,
	incident_id     INTEGER,
	priority        INTEGER NOT NULL DEFAULT 0,
	payload_json    TEXT    NOT NULL,
	created_at      INTEGER NOT NULL,
	attempts        INTEGER NOT NULL DEFAULT 0,
	next_attempt_at INTEGER NOT NULL,
	delivered_at    INTEGER,
	failed_at       INTEGER,
	last_error      TEXT
) STRICT;
CREATE INDEX alert_outbox_pending ON alert_outbox (delivered_at, next_attempt_at);
`

// migrate0001 creates the M1 tables and the install's one meta row.
func migrate0001(ctx context.Context, tx *sql.Tx, env migrationEnv) error {
	if _, err := tx.ExecContext(ctx, schema0001); err != nil {
		return err
	}
	installID, err := newID(env.rand, "in_")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO meta (id, install_id, created_at, generation) VALUES (1, ?, ?, 0)`,
		installID, env.now.UnixMilli())
	return err
}
