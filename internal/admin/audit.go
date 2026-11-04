package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// AuditEntry is one change in the audit log.
type AuditEntry struct {
	ID         int64           `json:"id" doc:"Rising with each entry; the cursor for paging."`
	Time       time.Time       `json:"time"`
	Actor      string          `json:"actor" doc:"Who made the change: an admin token's name, or system:<what> for kman itself."`
	Action     string          `json:"action" doc:"Such as admin_token.create or settings.set."`
	TargetKind string          `json:"target_kind"`
	TargetID   string          `json:"target_id"`
	IncidentID int64           `json:"incident_id,omitempty"`
	Before     json.RawMessage `json:"before,omitempty" doc:"The target before the change, where the action records it. Never a secret."`
	After      json.RawMessage `json:"after,omitempty" doc:"The target after the change, where the action records it. Never a secret."`
	ClientIP   string          `json:"client_ip,omitempty"`
}

// AuditQuery picks a page of the audit log. Entries always come oldest
// first.
type AuditQuery struct {
	After  int64 `query:"after" doc:"Return the entries after this id, from the start of the log when 0."`
	Limit  int   `query:"limit" doc:"The most entries to return, 1 to 1000. Default 100."`
	Latest bool  `query:"latest" doc:"Return the newest entries instead. after must then be 0."`
}

// AuditPage is a page of the audit log.
type AuditPage struct {
	Entries []AuditEntry `json:"entries"`
	More    bool         `json:"more" doc:"Whether the log holds more entries beyond this page: after it, or before it with latest."`
}

const (
	defaultAuditLimit = 100
	maxAuditLimit     = 1000
)

// Audit returns a page of the audit log.
func (s *Service) Audit(ctx context.Context, q AuditQuery) (AuditPage, error) {
	switch {
	case q.Limit < 0 || q.Limit > maxAuditLimit:
		return AuditPage{}, errorf(CodeInvalid, "limit must be from 1 to %d", maxAuditLimit)
	case q.After < 0:
		return AuditPage{}, errorf(CodeInvalid, "after must not be negative")
	case q.Latest && q.After != 0:
		return AuditPage{}, errorf(CodeInvalid, "latest pages back from the newest entry, so it takes no after")
	}
	if q.Limit == 0 {
		q.Limit = defaultAuditLimit
	}
	out := AuditPage{Entries: []AuditEntry{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		var es []store.AuditEntry
		var err error
		if q.Latest {
			es, err = tx.LatestAuditEntries(q.Limit + 1)
			if len(es) > q.Limit {
				es, out.More = es[1:], true
			}
		} else {
			es, err = tx.AuditEntries(q.After, q.Limit+1)
			if len(es) > q.Limit {
				es, out.More = es[:q.Limit], true
			}
		}
		for _, e := range es {
			out.Entries = append(out.Entries, AuditEntry{
				ID: e.ID, Time: e.TS, Actor: e.Actor, Action: e.Action, TargetKind: e.TargetKind, TargetID: e.TargetID,
				IncidentID: e.IncidentID, Before: rawOrNil(e.BeforeJSON), After: rawOrNil(e.AfterJSON), ClientIP: e.ClientIP,
			})
		}
		return err
	})
	return out, err
}

func rawOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}
