package adminapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

func TestTheAPIServesABackupAsAFileAndAuditsIt(t *testing.T) {
	s := newServer(t)
	r := s.do(t, s.owner, "POST", "/admin/v1/backup", "")
	if r.status != http.StatusOK {
		t.Fatalf("status %d: %.200s", r.status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	if cd := r.header.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="kman-backup-`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if cl := r.header.Get("Content-Length"); cl == "" || cl == "0" {
		t.Errorf("Content-Length = %q, want the size", cl)
	}
	path := filepath.Join(t.TempDir(), "got.db")
	if err := os.WriteFile(path, r.body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InspectBackup(t.Context(), path); err != nil {
		t.Errorf("what the API served is not a kman database: %v", err)
	}
	var page admin.AuditPage
	s.do(t, s.owner, "GET", "/admin/v1/audit?latest=true&limit=1", "").decode(t, 200, &page)
	if len(page.Entries) != 1 || page.Entries[0].Action != "backup.create" || page.Entries[0].Actor != "owner" {
		t.Errorf("newest audit entry = %+v, want owner's backup.create", page.Entries)
	}
}

func TestABackupNeedsAnAdminToken(t *testing.T) {
	s := newServer(t)
	for _, tok := range []string{"", "kma_nope"} {
		if r := s.do(t, tok, "POST", "/admin/v1/backup", ""); r.status != http.StatusUnauthorized || strings.Contains(string(r.body), "SQLite") {
			t.Errorf("token %q: %d %.100s, want 401 and no database", tok, r.status, r.body)
		}
	}
}

func TestTheAPIPrunesAndRefusesAFutureDate(t *testing.T) {
	s := newServer(t)
	var got admin.PruneResult
	s.do(t, s.owner, "POST", "/admin/v1/prune", `{"before":"2026-11-01T00:00:00Z"}`).decode(t, 200, &got)
	if got.Before.Format("2006-01-02") != "2026-11-01" {
		t.Errorf("PruneResult = %+v", got)
	}
	for name, body := range map[string]string{
		"future":  `{"before":"2027-01-01T00:00:00Z"}`,
		"no date": `{}`,
		"unknown": `{"before":"2026-11-01T00:00:00Z","everything":true}`,
	} {
		r := s.do(t, s.owner, "POST", "/admin/v1/prune", body)
		if r.status != http.StatusBadRequest || r.errorCode(t) != "invalid" {
			t.Errorf("%s: %d %s, want 400 invalid", name, r.status, r.body)
		}
	}
	var page admin.AuditPage
	s.do(t, s.owner, "GET", "/admin/v1/audit?latest=true&limit=1", "").decode(t, 200, &page)
	var after json.RawMessage = page.Entries[0].After
	if page.Entries[0].Action != "data.prune" || !strings.Contains(string(after), `"audit_log":false`) {
		t.Errorf("newest audit entry = %+v", page.Entries[0])
	}
}
