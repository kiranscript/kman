package alerts

import (
	"strconv"

	"github.com/kirank55/kman/internal/store"
)

// auditAction is how an audit action is alerted on.
type auditAction struct {
	severity Severity
	title    string
}

// auditActions is how much each action of the audit log matters. It is a
// table, so that a new action is a deliberate choice: one this kman does not
// know is low, which a default channel does not take.
var auditActions = map[string]auditAction{
	"admin_token.create":    {High, "Admin token created"},
	"admin_token.rotate":    {High, "Admin token rotated"},
	"admin_token.revoke":    {High, "Admin token revoked"},
	"credential.compromise": {High, "Credential replaced as compromised"},
	"credential.create":     {Medium, "Credential added"},
	"credential.update":     {Medium, "Credential changed"},
	"credential.delete":     {Medium, "Credential removed"},
	"key.revoke":            {High, "Key revoked"},
	"key.disable":           {Medium, "Key disabled"},
	"key.enable":            {Medium, "Key enabled"},
	"key.rotate":            {Medium, "Key rotated"},
	"key.create":            {Low, "Key created"},
	"key.update":            {Low, "Key changed"},
	"data.prune":            {Medium, "Data pruned"},
	"backup.create":         {Medium, "Backup taken"},
	"alert_channel.create":  {Medium, "Alert channel added"},
	"alert_channel.delete":  {Medium, "Alert channel removed"},
	"upstream.create":       {Low, "Upstream added"},
	"upstream.update":       {Low, "Upstream changed"},
	"upstream.sync":         {Low, "Upstream synced"},
	"upstream.delete":       {Low, "Upstream removed"},
	"settings.set":          {Low, "Settings changed"},
	"price_override.set":    {Low, "Price override set"},
	"price_override.delete": {Low, "Price override removed"},
}

// AlertAudit is the audit log's producer: admin calls it in the transaction
// that appends e, so the alert and the change commit together, and wakes the
// dispatcher (Wake) once it has committed. The alert says who did what to
// which target, and never what the entry recorded of the change: its before
// and after are not copied.
func (s *Service) AlertAudit(tx *store.Tx, e store.AuditEntry) error {
	act, ok := auditActions[e.Action]
	if !ok {
		act = auditAction{Low, e.Action}
	}
	a := Alert{
		Kind: "audit", Severity: act.severity, Title: act.title, Time: e.TS,
		Evidence: []Fact{{"Actor", e.Actor}, {"Action", e.Action}, {"Target", e.TargetKind + " " + e.TargetID}},
	}
	if e.ClientIP != "" {
		a.Evidence = append(a.Evidence, Fact{"From", e.ClientIP})
	}
	a.Evidence = append(a.Evidence, Fact{"Audit entry", strconv.FormatInt(e.ID, 10)})
	a.IncidentID = e.IncidentID
	switch e.TargetKind {
	case "key":
		a.KeyID = e.TargetID
		if err := nameKey(tx, &a); err != nil {
			return err
		}
		a.Commands = append(a.Commands, "kman key show "+e.TargetID)
		if e.Action == "key.disable" {
			a.Commands = append(a.Commands, "kman key enable "+e.TargetID)
		}
	case "admin_token":
		a.Commands = append(a.Commands, "kman admin-token list")
	}
	a.Commands = append(a.Commands, "kman audit --limit 20")
	return s.NotifyTx(tx, a)
}
