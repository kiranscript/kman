package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The prune methods are seeded through the repos where there is one, and with SQL
// for the alert outbox, whose repo belongs to the alerts module.

var cutoff = time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)

func TestPruneEventsDeletesOnlyEventsOlderThanTheCutoff(t *testing.T) {
	s := openTest(t, t.TempDir())
	do := func(fn func(*Tx) error) {
		t.Helper()
		if err := s.Do(context.Background(), fn); err != nil {
			t.Fatal(err)
		}
	}
	do(func(tx *Tx) error {
		return tx.AddEvents([]EventRow{
			{Time: cutoff.Add(-time.Hour), Kind: "request", KeyID: "kkkk1111", Status: 200},
			{Time: cutoff.Add(-time.Millisecond), Kind: "request", KeyID: "kkkk1111", Status: 200},
			{Time: cutoff, Kind: "request", KeyID: "kkkk1111", Status: 200},
			{Time: cutoff.Add(time.Hour), Kind: "blocked", Status: 401, BlockedCode: "invalid_key"},
		})
	})
	var n int64
	do(func(tx *Tx) (err error) { n, err = tx.PruneEvents(cutoff); return })
	if n != 2 {
		t.Errorf("PruneEvents deleted %d, want 2", n)
	}
	var left []EventRow
	do(func(tx *Tx) (err error) { left, err = tx.EventsSince(time.Time{}); return })
	if len(left) != 2 || !left[0].Time.Equal(cutoff) {
		t.Errorf("events left = %+v, want the one at the cutoff and the later one", left)
	}
	// Pruning events leaves the hourly usage they counted into.
	var usage []UsageRow
	do(func(tx *Tx) (err error) { usage, err = tx.UsageSince(time.Time{}); return })
	if len(usage) == 0 {
		t.Error("pruning events removed hourly usage")
	}
}

func TestPruneUsageHourlyKeepsAnHourThatReachesPastTheCutoff(t *testing.T) {
	s := openTest(t, t.TempDir())
	h := func(hour int) time.Time { return time.Date(2026, 10, 1, hour, 0, 0, 0, time.UTC) }
	err := s.Do(context.Background(), func(tx *Tx) error {
		return tx.AddEvents([]EventRow{
			{Time: h(10), Kind: "request", KeyID: "kkkk1111", Status: 200},
			{Time: h(11), Kind: "request", KeyID: "kkkk1111", Status: 200},
			{Time: h(12), Kind: "request", KeyID: "kkkk1111", Status: 200}, // 12:00 to 13:00 holds the cutoff, 12:30
			{Time: h(13), Kind: "request", KeyID: "kkkk1111", Status: 200},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	var left []UsageRow
	err = s.Do(context.Background(), func(tx *Tx) (err error) {
		if n, err = tx.PruneUsageHourly(cutoff); err != nil {
			return err
		}
		left, err = tx.UsageSince(time.Time{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(left) != 2 || !left[0].Hour.Equal(h(12)) {
		t.Errorf("deleted %d, left %+v: want hours 10 and 11 gone, 12 and 13 kept", n, left)
	}
}

func TestPruneDeliveredAlertsKeepsWhatIsStillPendingOrFailed(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO data_keys (dek_id, wrapped, nonce, kek_fingerprint, created_at) VALUES ('dek_1', x'00', x'00', 'f', 1)`)
	exec(`INSERT INTO alert_channels (id, name, kind, config_ciphertext, nonce, dek_id, display_host, min_severity, created_at, updated_at, generation)
		VALUES ('ch_1', 'slack', 'slack', x'00', x'00', 'dek_1', 'hooks.slack.com', 'medium', 1, 1, 1)`)
	old, recent := toMilli(cutoff.Add(-time.Hour)), toMilli(cutoff.Add(time.Hour))
	insert := func(delivered, failed any, created int64) {
		exec(`INSERT INTO alert_outbox (channel_id, kind, payload_json, created_at, next_attempt_at, delivered_at, failed_at)
			VALUES ('ch_1', 'incident', '{}', ?, ?, ?, ?)`, created, created, delivered, failed)
	}
	insert(old, nil, old)    // delivered long ago: pruned
	insert(recent, nil, old) // delivered after the cutoff: kept
	insert(nil, nil, old)    // still pending, however old: kept
	insert(nil, old, old)    // gave up, never delivered: kept for the operator to see
	var n int64
	if err := s.Do(ctx, func(tx *Tx) (err error) { n, err = tx.PruneDeliveredAlerts(cutoff); return }); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM alert_outbox`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if n != 1 || left != 3 {
		t.Errorf("deleted %d, %d left: want 1 deleted, 3 left", n, left)
	}
}

func TestPruneAuditLogDeletesOlderEntriesAndClosesTheGuard(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	appendTestAudit(t, s, 1000)
	appendTestAudit(t, s, 2000)
	appendTestAudit(t, s, 3000)
	var n int64
	err := s.Do(ctx, func(tx *Tx) (err error) { n, err = tx.PruneAuditLog(fromMilli(2000)); return })
	if err != nil {
		t.Fatalf("PruneAuditLog: %v", err)
	}
	if n != 1 || auditCount(t, s) != 2 {
		t.Errorf("deleted %d, %d left: want 1 deleted (the one before the cutoff), 2 left", n, auditCount(t, s))
	}
	// The guard is closed again: the log refuses a delete once more.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE ts < 5000`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("a plain DELETE after the prune: %v, want the append-only refusal", err)
	}
	var guard *int64
	if err := s.db.QueryRowContext(ctx, `SELECT audit_prune_before FROM meta`).Scan(&guard); err != nil || guard != nil {
		t.Errorf("audit_prune_before = %v, %v after the prune, want NULL", guard, err)
	}
}

func TestAnAuditPruneThatFailsMidwayChangesNothing(t *testing.T) {
	s := openTest(t, t.TempDir())
	appendTestAudit(t, s, 1000)
	appendTestAudit(t, s, 2000)
	boom := errors.New("boom")
	err := s.Do(context.Background(), func(tx *Tx) error {
		if _, err := tx.PruneAuditLog(fromMilli(5000)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Do: %v", err)
	}
	if n := auditCount(t, s); n != 2 {
		t.Errorf("%d audit entries left after a rolled-back prune, want 2", n)
	}
	var guard *int64
	if err := s.db.QueryRowContext(context.Background(), `SELECT audit_prune_before FROM meta`).Scan(&guard); err != nil || guard != nil {
		t.Errorf("audit_prune_before = %v, %v after a rollback, want NULL", guard, err)
	}
}

func TestReclaimGivesBackThePagesAPruneFreed(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	rows := make([]EventRow, 3000)
	for i := range rows {
		rows[i] = EventRow{Time: cutoff.Add(-time.Hour), Kind: "request", KeyID: "kkkk1111", Status: 200, ClientIP: strings.Repeat("x", 200)}
	}
	if err := s.Do(ctx, func(tx *Tx) error { return tx.AddEvents(rows) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Do(ctx, func(tx *Tx) error { _, err := tx.PruneEvents(cutoff); return err }); err != nil {
		t.Fatal(err)
	}
	free := func() (n int64) {
		if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := free()
	if before == 0 {
		t.Fatal("the prune freed no pages: the test is too small")
	}
	if err := s.Reclaim(ctx); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if after := free(); after >= before {
		t.Errorf("free pages %d -> %d: Reclaim gave none back", before, after)
	}
}
