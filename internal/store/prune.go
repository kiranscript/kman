package store

import (
	"context"
	"fmt"
	"time"
)

// The prune methods delete what retention (SPEC §14) lets go. Each returns how
// many rows it deleted. Daily spend and incidents are never pruned, so there is
// no method for them.

// PruneEvents deletes the events older than before. It leaves usage_hourly,
// which counted them into hours as they arrived.
func (tx *Tx) PruneEvents(before time.Time) (int64, error) {
	return tx.deleted("prune events", `DELETE FROM events WHERE ts < ?`, toMilli(before))
}

// PruneUsageHourly deletes the hours that ended at or before the given time. An
// hour that reaches past it is kept whole, since a row cannot be cut in half.
func (tx *Tx) PruneUsageHourly(before time.Time) (int64, error) {
	return tx.deleted("prune hourly usage", `DELETE FROM usage_hourly WHERE hour + ? <= ?`, hourMS, toMilli(before))
}

// PruneDeliveredAlerts deletes the alerts delivered before the given time. An
// alert that is still pending, or that was given up on, is kept: the operator has
// not seen what it said.
func (tx *Tx) PruneDeliveredAlerts(before time.Time) (int64, error) {
	return tx.deleted("prune delivered alerts", `DELETE FROM alert_outbox WHERE delivered_at IS NOT NULL AND delivered_at < ?`, toMilli(before))
}

// PruneAuditLog deletes the audit entries older than before: the one thing that
// may delete from the audit log, and only inside this guard. It sets
// meta.audit_prune_before, which opens the append-only trigger for rows older
// than it, deletes, and closes it again, all in this transaction, so a prune that
// fails or is rolled back leaves the log whole and the guard shut. The caller
// appends the entry that records the prune, in the same transaction.
func (tx *Tx) PruneAuditLog(before time.Time) (int64, error) {
	if _, err := tx.tx.ExecContext(tx.ctx, `UPDATE meta SET audit_prune_before = ? WHERE id = 1`, toMilli(before)); err != nil {
		return 0, typed(err, "prune the audit log")
	}
	n, err := tx.deleted("prune the audit log", `DELETE FROM audit_log WHERE ts < ?`, toMilli(before))
	if err != nil {
		return 0, err
	}
	if _, err := tx.tx.ExecContext(tx.ctx, `UPDATE meta SET audit_prune_before = NULL WHERE id = 1`); err != nil {
		return 0, typed(err, "prune the audit log")
	}
	return n, nil
}

func (tx *Tx) deleted(what, query string, args ...any) (int64, error) {
	res, err := tx.tx.ExecContext(tx.ctx, query, args...)
	if err != nil {
		return 0, typed(err, what)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, typed(err, what)
	}
	return n, nil
}

// reclaimPages is how many free pages one Reclaim step gives back to the file
// system, so a large prune does not hold the writer for long.
const reclaimPages = 2000

// Reclaim gives the pages a prune freed back to the file system, which is what
// auto_vacuum=INCREMENTAL is for, in steps small enough that the writer is
// never held for long. Without it a pruned database keeps its size.
func (s *Store) Reclaim(ctx context.Context) error {
	last := int64(-1)
	for {
		var free int64
		if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
			return typed(err, "reclaim space")
		}
		// A database that is not in incremental mode frees nothing: stop rather than spin.
		if free == 0 || (last >= 0 && free >= last) {
			return nil
		}
		last = free
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, reclaimPages)); err != nil {
			return typed(err, "reclaim space")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
