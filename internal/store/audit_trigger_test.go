package store

import (
	"context"
	"strings"
	"testing"
)

// The trigger is the guarantee, so this test goes around the repo, which has
// no way to change an entry, and tries plain SQL.
func TestAuditLogRefusesUpdateAndDelete(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	appendTestAudit(t, s, 1000)
	for _, stmt := range []string{
		`UPDATE audit_log SET actor = 'mallory'`,
		`DELETE FROM audit_log`,
	} {
		_, err := s.db.ExecContext(ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want the append-only refusal", stmt, err)
		}
	}
	if n := auditCount(t, s); n != 1 {
		t.Errorf("audit_log has %d rows, want 1", n)
	}
}

// The guarded prune: with meta.audit_prune_before set, rows older than it may
// be deleted, and nothing else.
func TestAuditLogAllowsTheGuardedPrune(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	appendTestAudit(t, s, 1000)
	appendTestAudit(t, s, 2000)
	if _, err := s.db.ExecContext(ctx, `UPDATE meta SET audit_prune_before = 2000`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE ts < 2000`); err != nil {
		t.Fatalf("pruning an old row: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE ts >= 2000`); err == nil {
		t.Error("deleting a row newer than audit_prune_before succeeded")
	}
	if n := auditCount(t, s); n != 1 {
		t.Errorf("audit_log has %d rows, want 1", n)
	}
}

func appendTestAudit(t *testing.T, s *Store, ts int64) {
	t.Helper()
	err := s.Do(context.Background(), func(tx *Tx) error {
		_, err := tx.AppendAudit(AuditEntry{TS: fromMilli(ts), Actor: "owner", Action: "test", TargetKind: "test", TargetID: "x"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func auditCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
