package store

import (
	"database/sql"
	"time"
)

// AuditEntry is one change recorded in the audit log. Entries are only ever
// appended: a trigger refuses every UPDATE, and every DELETE except the
// guarded prune. Before and After never hold secrets.
type AuditEntry struct {
	ID         int64 // assigned by AppendAudit
	TS         time.Time
	Actor      string // such as "owner" or "system:detect"
	Action     string
	TargetKind string
	TargetID   string
	IncidentID int64  // 0 for none
	BeforeJSON string // "" for none
	AfterJSON  string // "" for none
	ClientIP   string // "" for none
	RequestID  string // "" for none
}

const auditColumns = `id, ts, actor, action, target_kind, target_id, incident_id,
	before_json, after_json, client_ip, request_id`

// AppendAudit appends e and returns its id.
func (tx *Tx) AppendAudit(e AuditEntry) (int64, error) {
	res, err := tx.tx.ExecContext(tx.ctx, `INSERT INTO audit_log (ts, actor, action, target_kind, target_id,
		incident_id, before_json, after_json, client_ip, request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		toMilli(e.TS), e.Actor, e.Action, e.TargetKind, e.TargetID,
		nullInt(e.IncidentID), nullString(e.BeforeJSON), nullString(e.AfterJSON), nullString(e.ClientIP), nullString(e.RequestID))
	if err != nil {
		return 0, typed(err, "append audit entry")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, typed(err, "append audit entry")
	}
	return id, nil
}

// AuditEntries returns up to limit entries with ids above afterID, oldest
// first. Pass afterID 0 to start from the beginning.
func (tx *Tx) AuditEntries(afterID int64, limit int) ([]AuditEntry, error) {
	rows, err := tx.tx.QueryContext(tx.ctx,
		`SELECT `+auditColumns+` FROM audit_log WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, typed(err, "list audit entries")
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts int64
		var incident sql.NullInt64
		var before, after, ip, req sql.NullString
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &e.TargetKind, &e.TargetID,
			&incident, &before, &after, &ip, &req); err != nil {
			return nil, typed(err, "list audit entries")
		}
		e.TS = fromMilli(ts)
		e.IncidentID, e.BeforeJSON, e.AfterJSON, e.ClientIP, e.RequestID =
			incident.Int64, before.String, after.String, ip.String, req.String
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list audit entries")
	}
	return out, nil
}

// nullString stores "" as NULL.
func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// nullInt stores 0 as NULL.
func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }
