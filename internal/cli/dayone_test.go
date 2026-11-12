package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

func TestBackupWritesAnOwnerOnlyCopyOfTheDatabaseAndSaysWhatItNeeds(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	file := filepath.Join(t.TempDir(), "kman.backup")
	out := in.kman(t, "backup", file)
	if _, err := store.InspectBackup(t.Context(), file); err != nil {
		t.Fatalf("what backup wrote is not a kman database: %v", err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
			t.Errorf("backup mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	for _, want := range []string{file, "ciphertext", "master key"} {
		if !strings.Contains(out, want) {
			t.Errorf("backup printed %q, which lacks %q", out, want)
		}
	}
	if last := in.latestAudit(t); last.Action != "backup.create" || last.Actor != "owner" {
		t.Errorf("newest audit entry = %+v, want owner's backup.create", last)
	}
}

func TestBackupRefusesAFileThatExistsAndAsksTheServerForNothing(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	file := filepath.Join(t.TempDir(), "kman.backup")
	if err := os.WriteFile(file, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := in.latestAudit(t)
	r := in.run(t, nil, "", "backup", file)
	if r.code != 1 || !strings.Contains(r.stderr, "exists") {
		t.Errorf("backup over a file: exit %d, stderr %q, want 1 and that it exists", r.code, r.stderr)
	}
	if got := readFile(t, file); got != "precious" {
		t.Errorf("the file now holds %q", got)
	}
	if after := in.latestAudit(t); after.ID != before.ID {
		t.Error("the refused backup still asked the server, which audited it")
	}
}

func TestBackupToAFileThatCannotBeMadeLeavesNothingAndNoAuditOfSuccess(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	r := in.run(t, nil, "", "backup", filepath.Join(t.TempDir(), "no", "such", "dir", "kman.backup"))
	if r.code != 1 {
		t.Errorf("exit %d, want 1: %s", r.code, r.stderr)
	}
	// Without a token the server refuses, and no file is made.
	file := filepath.Join(t.TempDir(), "kman.backup")
	r = in.run(t, map[string]string{"KMAN_ADMIN_TOKEN": "kma_wrong", "KMAN_ADMIN_URL": in.url}, "", "backup", file)
	if r.code != 1 {
		t.Errorf("exit %d with a wrong token, want 1", r.code)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("a refused backup left a file")
	}
}

func TestBackupTakesExactlyOneFile(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	if r := in.run(t, nil, "", "backup"); r.code != 1 || !strings.Contains(r.stderr, "usage: kman backup") {
		t.Errorf("backup with no file: exit %d, %q", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "backup", "a", "b"); r.code != 1 {
		t.Errorf("backup with two files: exit %d", r.code)
	}
}

func TestPruneReportsWhatItDeletedAndRecordsIt(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	var res admin.PruneResult
	in.kmanJSON(t, &res, "prune", "--before", "2026-11-01")
	if res.Before.Format("2006-01-02T15:04:05Z") != "2026-11-01T00:00:00Z" {
		t.Errorf("the date 2026-11-01 became %v, want midnight UTC", res.Before)
	}
	out := in.kman(t, "prune", "--before", "2026-11-01T00:00:00Z")
	for _, want := range []string{"events", "hourly usage", "alerts", "audit log is untouched", "Daily spend and incidents are kept"} {
		if !strings.Contains(out, want) {
			t.Errorf("prune printed %q, which lacks %q", out, want)
		}
	}
	if last := in.latestAudit(t); last.Action != "data.prune" || last.Actor != "owner" {
		t.Errorf("newest audit entry = %+v", last)
	}
}

func TestPruneOfTheAuditLogNeedsItsFlagAndKeepsTheRecordOfIt(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, ts := range []int64{1_000, 2_000} { // two entries from long ago
		in.sqlExec(t, `INSERT INTO audit_log (ts, actor, action, target_kind, target_id) VALUES (?, 'owner', 'old', 'x', 'y')`, ts)
	}
	count := func() (n int) {
		var page admin.AuditPage
		in.kmanJSON(t, &page, "audit", "--limit", "1000")
		return len(page.Entries)
	}
	start := count() // the two, and the install's own

	in.kman(t, "prune", "--before", "2026-11-01")
	if got := count(); got != start+1 {
		t.Fatalf("%d audit entries after a prune without --audit-log, want %d: only the record of the prune is new", got, start+1)
	}
	var res admin.PruneResult
	in.kmanJSON(t, &res, "prune", "--before", "2026-11-01", "--audit-log")
	if res.AuditEntries != 2 {
		t.Errorf("prune --audit-log deleted %d entries, want the 2 from long ago", res.AuditEntries)
	}
	out := in.kman(t, "prune", "--before", "2026-11-01", "--audit-log")
	if !strings.Contains(out, "0 audit log entries deleted") {
		t.Errorf("prune --audit-log printed %q", out)
	}
	// Three prunes recorded themselves, and the old two are gone.
	if got := count(); got != start+3-2 {
		t.Errorf("%d audit entries at the end, want %d: the old two gone, every record of a prune kept", got, start+3-2)
	}
	last := in.latestAudit(t)
	if last.Action != "data.prune" || !strings.Contains(string(last.After), `"audit_log": true`) {
		t.Errorf("the newest entry = %+v, want the record of an audit prune", last)
	}
}

func TestPruneNeedsADateAndRefusesAFutureOne(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"prune"}, "--before"},
		{[]string{"prune", "--before", "last tuesday"}, "date"},
		{[]string{"prune", "--before", "2027-01-01"}, "future"},
		{[]string{"prune", "--before", "2026-11-01", "extra"}, "usage"},
	} {
		r := in.run(t, nil, "", tc.args...)
		if r.code != 1 || !strings.Contains(r.stderr, tc.want) {
			t.Errorf("kman %v: exit %d, stderr %q, want 1 and %q", tc.args, r.code, r.stderr, tc.want)
		}
	}
}

func (in *install) latestAudit(t *testing.T) admin.AuditEntry {
	t.Helper()
	var page admin.AuditPage
	in.kmanJSON(t, &page, "audit", "--limit", "1")
	return page.Entries[0]
}

// A running kman that has found a newer release says so in the summary, so an
// operator who opted into update_check hears of it without asking.
func TestTheSummaryNamesANewerReleaseTheServerFound(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	checked := t0
	in.withUpdate(t, func() admin.UpdateStatus {
		return admin.UpdateStatus{Enabled: true, Current: "v0.1.0", Latest: "v0.2.0", Available: true, URL: "https://example.test/v0.2.0", CheckedAt: &checked}
	})
	out := in.summaryOf(t, nil, true)
	for _, want := range []string{"A newer kman is available: v0.2.0", "you have v0.1.0", "https://example.test/v0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary lacks %q:\n%s", want, out)
		}
	}
}

func TestTheSummaryIsQuietAboutUpdatesWhenTheCheckIsOffOrFindsNothing(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	if out := in.summaryOf(t, nil, true); strings.Contains(out, "newer kman") || strings.Contains(out, "update") {
		t.Errorf("with the check off the summary says:\n%s", out)
	}
	in.withUpdate(t, func() admin.UpdateStatus {
		return admin.UpdateStatus{Enabled: true, Current: "v0.2.0", Latest: "v0.2.0"}
	})
	if out := in.summaryOf(t, nil, true); strings.Contains(out, "newer kman") {
		t.Errorf("with nothing newer the summary says:\n%s", out)
	}
}
