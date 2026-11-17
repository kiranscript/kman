package admin_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

func TestPruneDeletesEventsAndUsageBeforeTheDateAndIsAudited(t *testing.T) {
	in := newInstall(t)
	in.seedHistory(t, 10)
	cutoff := in.now.Add(-5*24*time.Hour - 30*time.Minute) // events 5 to 9 days back are older
	got, err := in.svc.Prune(as("owner"), admin.PruneRequest{Before: cutoff})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got.Events != 5 || got.HourlyUsage != 4 || got.AuditEntries != 0 {
		t.Errorf("Prune = %+v, want 5 events and 4 hours gone, no audit entries", got)
	}
	if n := in.count(t, "events"); n != 5 {
		t.Errorf("%d events left, want 5", n)
	}
	e := in.latest(t, 1)[0]
	if summary(e) != "owner data.prune store/install" || e.ClientIP != "127.0.0.1" {
		t.Errorf("audit entry = %s from %s, want owner's data.prune", summary(e), e.ClientIP)
	}
	if string(e.After) == "" {
		t.Error("the audit entry records no counts")
	}
}

func TestPruneNeverTouchesDailySpend(t *testing.T) {
	in := newInstall(t)
	in.seedHistory(t, 10)
	if _, err := in.svc.Prune(as("owner"), admin.PruneRequest{Before: in.now}); err != nil {
		t.Fatal(err)
	}
	if n := in.spendRows(t); n != 10 {
		t.Errorf("%d daily spend rows left, want all 10: spend is kept forever", n)
	}
}

func TestPruneLeavesTheAuditLogAloneUnlessAskedAndThenKeepsTheRecordOfIt(t *testing.T) {
	in := newInstall(t)
	// Entries from before: the install's own first entry is at t0, then:
	in.now = t0.Add(48 * time.Hour)
	in.addToken(t, "second") // an entry 2 days after t0
	in.now = t0.Add(72 * time.Hour)
	cutoff := t0.Add(24 * time.Hour) // the first entry, at t0, is older than this

	before := len(in.allAudit(t))
	if _, err := in.svc.Prune(as("owner"), admin.PruneRequest{Before: cutoff}); err != nil {
		t.Fatal(err)
	}
	if got := len(in.allAudit(t)); got != before+1 {
		t.Errorf("%d audit entries after a prune that did not ask for the log, want %d: only the prune's own record is new", got, before+1)
	}

	got, err := in.svc.Prune(as("owner"), admin.PruneRequest{Before: cutoff, AuditLog: true})
	if err != nil {
		t.Fatalf("Prune with the audit log: %v", err)
	}
	if got.AuditEntries != 1 {
		t.Errorf("Prune = %+v, want one audit entry pruned", got)
	}
	entries := in.allAudit(t)
	for _, e := range entries {
		if e.Time.Before(cutoff) {
			t.Errorf("entry %s from %v survived the audit prune", summary(e), e.Time)
		}
	}
	last := entries[len(entries)-1]
	if summary(last) != "owner data.prune store/install" || !last.Time.Equal(in.now) {
		t.Errorf("the newest entry is %s at %v, want the record of the prune", summary(last), last.Time)
	}
}

func TestPruneRefusesNoDateAndADateInTheFuture(t *testing.T) {
	in := newInstall(t)
	for name, req := range map[string]admin.PruneRequest{
		"no date": {},
		"future":  {Before: in.now.Add(time.Second)},
	} {
		if _, err := in.svc.Prune(as("owner"), req); code(err) != admin.CodeInvalid {
			t.Errorf("%s: Prune = %v, want an invalid error", name, err)
		}
	}
	if _, err := in.svc.Prune(context.Background(), admin.PruneRequest{Before: in.now}); code(err) != admin.CodeUnauthorized {
		t.Errorf("Prune with no caller = %v, want unauthorized", err)
	}
}

func TestRetainPrunesToTheDefaultWindowsAndAuditsAsTheSystem(t *testing.T) {
	in := newInstall(t)
	in.seedHistory(t, 500)
	got, err := in.svc.Retain(context.Background(), admin.DefaultRetention)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	// Events: the last 30 days stay. Hourly usage: 400 days.
	if got.Events < 468 || got.Events > 471 {
		t.Errorf("Retain deleted %d events, want about 470 (500 days seeded, 30 kept)", got.Events)
	}
	if n := in.count(t, "events"); n < 29 || n > 31 {
		t.Errorf("%d events left, want about 30 days' worth", n)
	}
	if n := in.count(t, "usage_hourly"); n < 399 || n > 401 {
		t.Errorf("%d hourly usage rows left, want about 400", n)
	}
	if n := in.spendRows(t); n != 500 {
		t.Errorf("%d daily spend rows left, want all 500", n)
	}
	if e := in.latest(t, 1)[0]; summary(e) != "system:retention data.prune store/install" {
		t.Errorf("the newest audit entry is %q, want the system's prune", summary(e))
	}
	// A second pass has nothing to delete, so it says nothing.
	n := len(in.allAudit(t))
	if _, err := in.svc.Retain(context.Background(), admin.DefaultRetention); err != nil {
		t.Fatal(err)
	}
	if len(in.allAudit(t)) != n {
		t.Error("a retention pass that deleted nothing wrote an audit entry")
	}
}

func TestRetentionRunsAtStartupAndThenDailyUntilStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.clock = time.Now
		start := time.Now()
		in.now = start
		in.seedHistoryAt(t, start, 40) // 40 days of events, a day apart
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { in.svc.RunRetention(ctx, admin.DefaultRetention, nil); close(done) }()
		synctest.Wait()
		if n := in.count(t, "events"); n < 29 || n > 31 {
			t.Fatalf("%d events after startup, want 30 days' worth: the loop did not run at startup", n)
		}
		// A day on, one more day has aged out.
		time.Sleep(24*time.Hour + time.Minute)
		synctest.Wait()
		if n := in.count(t, "events"); n < 28 || n > 30 {
			t.Errorf("%d events a day later, want one fewer", n)
		}
		cancel()
		<-done
	})
}

func TestBackupStreamsAConsistentCopyAndIsAudited(t *testing.T) {
	in := newInstall(t)
	d, err := in.svc.Backup(as("owner"))
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if d.Size <= 0 || d.Name == "" {
		t.Errorf("Backup = size %d name %q, want both", d.Size, d.Name)
	}
	copied := in.drain(t, d)
	info, err := store.InspectBackup(context.Background(), copied)
	if err != nil {
		t.Fatalf("the streamed copy is not a kman database: %v", err)
	}
	if info.SchemaVersion != store.SchemaVersion {
		t.Errorf("copy schema = %d", info.SchemaVersion)
	}
	if e := in.latest(t, 1)[0]; summary(e) != "owner backup.create store/install" {
		t.Errorf("the newest audit entry is %q, want owner's backup.create", summary(e))
	}
	if left := in.stagingLeft(t); len(left) != 0 {
		t.Errorf("a finished backup left %v in the data directory", left)
	}
	if _, err := in.svc.Backup(context.Background()); code(err) != admin.CodeUnauthorized {
		t.Errorf("Backup with no caller = %v, want unauthorized", err)
	}
}

func TestAnAbandonedBackupDownloadLeavesNothingBehind(t *testing.T) {
	in := newInstall(t)
	d, err := in.svc.Backup(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	d.Body.Close() // the client went away before reading
	if left := in.stagingLeft(t); len(left) != 0 {
		t.Errorf("an abandoned backup left %v in the data directory", left)
	}
}
