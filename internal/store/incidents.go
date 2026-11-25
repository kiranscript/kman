package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Severity is how much a detection or an incident matters (SPEC §10): the same
// four names an alert channel's min_severity takes.
type Severity string

// The severities, lowest first.
const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Rank orders severities: 1 for low to 4 for critical, and 0 for a name that is
// not one.
func (s Severity) Rank() int {
	switch s {
	case SeverityLow:
		return 1
	case SeverityMedium:
		return 2
	case SeverityHigh:
		return 3
	case SeverityCritical:
		return 4
	}
	return 0
}

// ParseSeverity reads a severity by name, in any case.
func ParseSeverity(name string) (Severity, error) {
	s := Severity(strings.ToLower(strings.TrimSpace(name)))
	if s.Rank() == 0 {
		return "", fmt.Errorf("unknown severity %q: use low, medium, high or critical", name)
	}
	return s, nil
}

// The states of an incident. It opens open, is acknowledged by an operator, and
// ends resolved.
const (
	IncidentOpen         = "open"
	IncidentAcknowledged = "acknowledged"
	IncidentResolved     = "resolved"
)

// How a resolved incident ended: the operator confirmed it, called it a false
// positive, or nobody acknowledged it for long enough that kman let it go.
const (
	ResolutionConfirmed     = "confirmed"
	ResolutionFalsePositive = "false_positive"
	ResolutionExpired       = "expired"
)

// How long an incident may go without a firing before it is quiet, and how long
// an unacknowledged one that is not critical may be quiet before it expires
// (SPEC §10). They are fixed, and the admin API and detect both read them here.
const (
	IncidentQuietAfter  = time.Hour
	IncidentExpireAfter = 7 * 24 * time.Hour
)

// Incident is the open case for one key (#88). A key has at most one
// unresolved incident; each rule firing is a detection attached to it.
type Incident struct {
	ID    int64 // assigned by CreateIncident; never reused
	KeyID string
	State string
	// Severity is the highest severity among the detections.
	Severity Severity
	// AlertedSeverity is the severity the last alert queued for the incident
	// named: "" before any.
	AlertedSeverity Severity
	// RulesJSON is a JSON array of the rules that have fired, in order.
	RulesJSON string
	// Detections counts every detection raised, repeats included; rows are kept
	// for fewer.
	Detections  int64
	OpenedAt    time.Time
	LastFiredAt time.Time
	// AcknowledgedAt and By are zero and empty until an operator acknowledges.
	AcknowledgedAt time.Time
	AcknowledgedBy string
	// ResolvedAt, ResolvedBy and Resolution are set once, when it ends.
	ResolvedAt time.Time
	ResolvedBy string
	Resolution string
	// WouldDisableAt and WouldBlockCount are dry-run results (a later ticket).
	WouldDisableAt  time.Time
	WouldBlockCount int64
	Generation      int64 // set by the store
}

// Rules is the rules that have fired, in order. A value that is not a JSON array
// of strings reads as none.
func (i Incident) Rules() []string {
	var rules []string
	if err := json.Unmarshal([]byte(i.RulesJSON), &rules); err != nil {
		return nil
	}
	return rules
}

// Detection is one firing of one rule, attached to an incident.
type Detection struct {
	ID           int64 // assigned by AddDetection
	IncidentID   int64
	KeyID        string
	At           time.Time
	Rule         string
	Severity     Severity
	EvidenceJSON string // a JSON object, "{}" for none
}

const incidentColumns = `id, key_id, state, severity, alerted_severity, rules_json, detection_count, opened_at, last_fired_at,
	acknowledged_at, acknowledged_by, resolved_at, resolved_by, resolution, would_disable_at, would_block_count, generation`

// CreateIncident opens an incident for i.KeyID, in state open, with this
// transaction's generation. A key that already has an unresolved incident is
// ErrConflict. The id is assigned.
func (tx *Tx) CreateIncident(i Incident) (Incident, error) {
	gen, err := tx.Generation()
	if err != nil {
		return Incident{}, err
	}
	if i.RulesJSON == "" {
		i.RulesJSON = `[]`
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO incidents (key_id, state, severity, alerted_severity, rules_json, detection_count,
			opened_at, last_fired_at, generation) VALUES (?, 'open', ?, ?, ?, ?, ?, ?, ?) RETURNING `+incidentColumns,
		i.KeyID, i.Severity, nullString(string(i.AlertedSeverity)), i.RulesJSON, i.Detections, toMilli(i.OpenedAt), toMilli(i.LastFiredAt), gen)
	return scanIncident(row, "create incident for key "+i.KeyID)
}

// Incident returns the incident with this id, or ErrNotFound.
func (tx *Tx) Incident(id int64) (Incident, error) {
	row := tx.tx.QueryRowContext(tx.ctx, `SELECT `+incidentColumns+` FROM incidents WHERE id = ?`, id)
	return scanIncident(row, fmt.Sprintf("incident %d", id))
}

// UnresolvedIncident returns the key's open or acknowledged incident, or
// ErrNotFound when it has none.
func (tx *Tx) UnresolvedIncident(keyID string) (Incident, error) {
	row := tx.tx.QueryRowContext(tx.ctx, `SELECT `+incidentColumns+` FROM incidents WHERE key_id = ? AND state <> 'resolved'`, keyID)
	return scanIncident(row, "unresolved incident of key "+keyID)
}

// UpdateIncident writes the fields of i that change over its life: state,
// severity, alerted severity, rules, counters, acknowledgement and resolution.
// It does not change its key or when it opened.
func (tx *Tx) UpdateIncident(i Incident) (Incident, error) {
	gen, err := tx.Generation()
	if err != nil {
		return Incident{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE incidents SET state = ?, severity = ?, alerted_severity = ?, rules_json = ?, detection_count = ?,
			last_fired_at = ?, acknowledged_at = ?, acknowledged_by = ?, resolved_at = ?, resolved_by = ?, resolution = ?,
			would_disable_at = ?, would_block_count = ?, generation = ?
		WHERE id = ? RETURNING `+incidentColumns,
		i.State, i.Severity, nullString(string(i.AlertedSeverity)), i.RulesJSON, i.Detections, toMilli(i.LastFiredAt),
		nullMilli(i.AcknowledgedAt), nullString(i.AcknowledgedBy), nullMilli(i.ResolvedAt), nullString(i.ResolvedBy), nullString(i.Resolution),
		nullMilli(i.WouldDisableAt), i.WouldBlockCount, gen, i.ID)
	return scanIncident(row, fmt.Sprintf("update incident %d", i.ID))
}

// IncidentFilter picks incidents. The zero value picks every one.
type IncidentFilter struct {
	KeyID          string
	UnresolvedOnly bool
	// Before pages: only incidents with an id below it, when not zero.
	Before int64
	// Limit is how many to return, 0 for all.
	Limit int
}

// Incidents returns the incidents the filter picks, newest first.
func (tx *Tx) Incidents(f IncidentFilter) ([]Incident, error) {
	q := `SELECT ` + incidentColumns + ` FROM incidents WHERE 1 = 1`
	var args []any
	if f.KeyID != "" {
		q += ` AND key_id = ?`
		args = append(args, f.KeyID)
	}
	if f.UnresolvedOnly {
		q += ` AND state <> 'resolved'`
	}
	if f.Before != 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	q += ` ORDER BY id DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	return tx.incidents(q, args...)
}

// ExpirableIncidents returns open incidents that nobody acknowledged and that
// have not fired since quietBefore, other than critical ones, oldest first, at
// most limit. Acknowledged and critical incidents never expire.
func (tx *Tx) ExpirableIncidents(quietBefore time.Time, limit int) ([]Incident, error) {
	return tx.incidents(`SELECT `+incidentColumns+` FROM incidents
		WHERE state = 'open' AND severity <> 'critical' AND last_fired_at <= ? ORDER BY last_fired_at, id LIMIT ?`,
		toMilli(quietBefore), limit)
}

func (tx *Tx) incidents(query string, args ...any) ([]Incident, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, query, args...)
	if err != nil {
		return nil, typed(err, "list incidents")
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		i, err := scanIncident(rows, "list incidents")
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list incidents")
	}
	return out, nil
}

func scanIncident(row scanner, what string) (Incident, error) {
	var i Incident
	var opened, fired int64
	var alerted, ackBy, resBy, resolution sql.NullString
	var ack, resolved, wouldDisable sql.NullInt64
	err := row.Scan(&i.ID, &i.KeyID, &i.State, &i.Severity, &alerted, &i.RulesJSON, &i.Detections, &opened, &fired,
		&ack, &ackBy, &resolved, &resBy, &resolution, &wouldDisable, &i.WouldBlockCount, &i.Generation)
	if err != nil {
		return Incident{}, typed(err, what)
	}
	i.AlertedSeverity = Severity(alerted.String)
	i.OpenedAt, i.LastFiredAt = fromMilli(opened), fromMilli(fired)
	i.AcknowledgedAt, i.AcknowledgedBy = timeOf(ack), ackBy.String
	i.ResolvedAt, i.ResolvedBy, i.Resolution = timeOf(resolved), resBy.String, resolution.String
	i.WouldDisableAt = timeOf(wouldDisable)
	return i, nil
}

// AddDetection records a detection under its incident and returns it with its id.
func (tx *Tx) AddDetection(d Detection) (Detection, error) {
	if d.EvidenceJSON == "" {
		d.EvidenceJSON = `{}`
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO detections (incident_id, key_id, ts, rule, severity, evidence_json)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`, d.IncidentID, d.KeyID, toMilli(d.At), d.Rule, d.Severity, d.EvidenceJSON)
	if err := row.Scan(&d.ID); err != nil {
		return Detection{}, typed(err, fmt.Sprintf("add detection to incident %d", d.IncidentID))
	}
	return d, nil
}

// CountDetections returns how many detection rows an incident has, counting no
// further than limit, so its cost is bounded however many it has.
func (tx *Tx) CountDetections(incidentID int64, limit int) (int64, error) {
	var n int64
	if err := tx.tx.QueryRowContext(tx.ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM detections WHERE incident_id = ? LIMIT ?)`, incidentID, limit).Scan(&n); err != nil {
		return 0, typed(err, "count detections")
	}
	return n, nil
}

// Detections returns the detections recorded under an incident, oldest first, at
// most limit.
func (tx *Tx) Detections(incidentID int64, limit int) ([]Detection, error) {
	return tx.detections(`SELECT id, incident_id, key_id, ts, rule, severity, evidence_json
		FROM detections WHERE incident_id = ? ORDER BY id LIMIT ?`, incidentID, limit)
}

// NewestDetections returns the newest limit detections recorded under an
// incident, oldest first.
func (tx *Tx) NewestDetections(incidentID int64, limit int) ([]Detection, error) {
	ds, err := tx.detections(`SELECT id, incident_id, key_id, ts, rule, severity, evidence_json
		FROM detections WHERE incident_id = ? ORDER BY id DESC LIMIT ?`, incidentID, limit)
	slices.Reverse(ds)
	return ds, err
}

func (tx *Tx) detections(query string, incidentID int64, limit int) ([]Detection, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, query, incidentID, limit)
	if err != nil {
		return nil, typed(err, "list detections")
	}
	defer rows.Close()
	var out []Detection
	for rows.Next() {
		var d Detection
		var ts int64
		if err := rows.Scan(&d.ID, &d.IncidentID, &d.KeyID, &ts, &d.Rule, &d.Severity, &d.EvidenceJSON); err != nil {
			return nil, typed(err, "list detections")
		}
		d.At = fromMilli(ts)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list detections")
	}
	return out, nil
}

// AuditEntriesForIncident returns the audit entries that carry the incident's
// id, oldest first: with its detections, the incident's timeline.
func (tx *Tx) AuditEntriesForIncident(incidentID int64) ([]AuditEntry, error) {
	return tx.auditEntries(`SELECT `+auditColumns+` FROM audit_log WHERE incident_id = ? ORDER BY id`, incidentID)
}

// Evidence is what a detection saw, as detections.evidence_json holds it. An
// address is personal data (ADR 0010): Address, Network and Usual are the only
// fields that may hold one, so the admin API withholds exactly those unless
// expose_client_addresses is on. Observed, Baseline and Detail say numbers and
// names, and a rule never puts an address in them.
type Evidence struct {
	Address  string `json:"address,omitempty"`
	Network  string `json:"network,omitempty"`
	Country  string `json:"country,omitempty"`
	ASN      int64  `json:"asn,omitempty"`
	ASOrg    string `json:"as_org,omitempty"`
	Client   string `json:"client,omitempty"`
	Observed string `json:"observed,omitempty"`
	Baseline string `json:"baseline,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// Usual lists the networks a key normally uses, when a detection is about a
	// new network: they are addresses, so the admin API withholds them like
	// Address and Network.
	Usual string `json:"usual,omitempty"`
}
