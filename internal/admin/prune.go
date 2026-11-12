package admin

import (
	"context"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// Retention is how long the data that is pruned by age is kept (SPEC §14). Daily
// spend, incidents and the audit log are kept forever, so they have no window
// here; only kman prune --before touches the audit log, and only when asked.
type Retention struct {
	Events          time.Duration
	HourlyUsage     time.Duration
	DeliveredAlerts time.Duration // how long after delivery
}

// DefaultRetention is what an install keeps unless kman.yaml says
// retention: keep_everything.
var DefaultRetention = Retention{Events: 30 * 24 * time.Hour, HourlyUsage: 400 * 24 * time.Hour, DeliveredAlerts: 30 * 24 * time.Hour}

// retainEvery is how often RunRetention prunes: a day's worth is small.
const retainEvery = 24 * time.Hour

// retainRetry is how long RunRetention waits after a pass that failed.
const retainRetry = time.Hour

// PruneRequest asks to delete data older than a date.
type PruneRequest struct {
	Before   time.Time `json:"before" doc:"Delete what is older than this: events, hourly usage, and alerts delivered before it. Not in the future."`
	AuditLog bool      `json:"audit_log,omitempty" doc:"Also delete the audit log's entries older than before. The one thing that ever deletes from the audit log. The prune is recorded in the log, and that record stays."`
}

// PruneResult is what a prune deleted. Daily spend and incidents are never
// pruned.
type PruneResult struct {
	Before          time.Time `json:"before"`
	Events          int64     `json:"events" doc:"Events deleted."`
	HourlyUsage     int64     `json:"hourly_usage" doc:"Hourly usage rows deleted: those whose hour ended before the date."`
	DeliveredAlerts int64     `json:"delivered_alerts" doc:"Alerts deleted that were delivered before the date. Pending and failed alerts are kept."`
	AuditEntries    int64     `json:"audit_entries" doc:"Audit log entries deleted. 0 unless audit_log was set."`
}

// pruneAudit is what a prune records of itself.
type pruneAudit struct {
	PruneResult
	AuditLog bool `json:"audit_log"`
}

// Prune deletes data older than req.Before, in one transaction, and records the
// prune in the audit log. The audit log is pruned only when req asks, through the
// guard its trigger allows (store.Tx.PruneAuditLog); the record of this prune is
// appended after it and is never older than the date, so it stays.
func (s *Service) Prune(ctx context.Context, req PruneRequest) (PruneResult, error) {
	switch {
	case req.Before.IsZero():
		return PruneResult{}, errorf(CodeInvalid, "before is required: the date to prune up to, such as 2026-01-01")
	case req.Before.After(s.now()):
		return PruneResult{}, errorf(CodeInvalid, "before %s is in the future: a prune deletes what is older than a date that has passed", req.Before.UTC().Format(time.RFC3339))
	}
	out := PruneResult{Before: req.Before.UTC()}
	err := s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		if err := pruneData(tx, req.Before, req.AuditLog, &out); err != nil {
			return nil, err
		}
		return []audit{{action: "data.prune", kind: "store", id: "install", after: pruneAudit{out, req.AuditLog}}}, nil
	})
	if err != nil {
		return PruneResult{}, err
	}
	s.reclaim(ctx)
	return out, nil
}

// pruneData deletes everything older than before that a prune may, adding what
// it deleted to out.
func pruneData(tx *store.Tx, before time.Time, auditLog bool, out *PruneResult) (err error) {
	if out.Events, err = tx.PruneEvents(before); err != nil {
		return err
	}
	if out.HourlyUsage, err = tx.PruneUsageHourly(before); err != nil {
		return err
	}
	if out.DeliveredAlerts, err = tx.PruneDeliveredAlerts(before); err != nil {
		return err
	}
	if auditLog {
		out.AuditEntries, err = tx.PruneAuditLog(before)
	}
	return err
}

// reclaim gives a prune's freed pages back. The prune has committed, so a failure
// costs only the disk space, which the next one gets back.
func (s *Service) reclaim(ctx context.Context) { _ = s.store.Reclaim(ctx) }

// Retain prunes to the retention windows once, as kman itself ("system:retention"),
// and records it in the audit log if it deleted anything. It is housekeeping, not
// a command: it has no caller. It never prunes the audit log.
func (s *Service) Retain(ctx context.Context, r Retention) (PruneResult, error) {
	now := s.now()
	var out PruneResult
	err := s.store.Do(ctx, func(tx *store.Tx) (err error) {
		if out.Events, err = tx.PruneEvents(now.Add(-r.Events)); err != nil {
			return err
		}
		if out.HourlyUsage, err = tx.PruneUsageHourly(now.Add(-r.HourlyUsage)); err != nil {
			return err
		}
		if out.DeliveredAlerts, err = tx.PruneDeliveredAlerts(now.Add(-r.DeliveredAlerts)); err != nil {
			return err
		}
		if out.Events+out.HourlyUsage+out.DeliveredAlerts == 0 {
			return nil
		}
		after, err := marshalOrEmpty(retained{Retention: r.windows(), PruneResult: out})
		if err != nil {
			return err
		}
		entry := store.AuditEntry{TS: now, Actor: "system:retention", Action: "data.prune",
			TargetKind: "store", TargetID: "install", AfterJSON: after}
		if entry.ID, err = tx.AppendAudit(entry); err != nil {
			return err
		}
		if s.alerts != nil { // the audit log is an alert producer (SPEC §9), housekeeping included
			return s.alerts.AlertAudit(tx, entry)
		}
		return nil
	})
	if err != nil {
		return PruneResult{}, err
	}
	if out.Events+out.HourlyUsage+out.DeliveredAlerts > 0 {
		if s.alerts != nil {
			s.alerts.Wake()
		}
		s.reclaim(ctx)
	}
	return out, nil
}

// retained is what a retention pass records: the windows it applied, and what
// it deleted.
type retained struct {
	Retention retentionDays `json:"retention_days"`
	PruneResult
}

type retentionDays struct {
	Events          int `json:"events"`
	HourlyUsage     int `json:"hourly_usage"`
	DeliveredAlerts int `json:"delivered_alerts"`
}

func (r Retention) windows() retentionDays {
	d := func(x time.Duration) int { return int(x / (24 * time.Hour)) }
	return retentionDays{d(r.Events), d(r.HourlyUsage), d(r.DeliveredAlerts)}
}

// RunRetention prunes at startup and then daily, until ctx ends. report, if not
// nil, hears of each pass: what it deleted, or why it failed, which is tried
// again in an hour.
func (s *Service) RunRetention(ctx context.Context, r Retention, report func(PruneResult, error)) {
	for {
		wait := retainEvery
		res, err := s.Retain(ctx, r)
		if err != nil {
			wait = retainRetry
		}
		if report != nil && ctx.Err() == nil {
			report(res, err)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
