package adminapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

func TestARequestWithoutALiveAdminTokenIsRefused(t *testing.T) {
	s := newServer(t)
	for name, tok := range map[string]string{
		"no token":       "",
		"a wrong token":  "kma_notarealtoken",
		"a virtual key":  "kmn_Ab3dE6gH_0000000000000000000000000000000000000000000000",
		"owner with tag": s.owner + "x",
	} {
		r := s.do(t, tok, "GET", "/admin/v1/whoami", "")
		if r.status != http.StatusUnauthorized || r.errorCode(t) != "unauthorized" {
			t.Errorf("%s: status %d %s, want 401 unauthorized", name, r.status, r.body)
		}
		if !strings.HasPrefix(r.header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: WWW-Authenticate = %q, want Bearer", name, r.header.Get("WWW-Authenticate"))
		}
	}
}

func TestADashboardCookieIsNeverAccepted(t *testing.T) {
	s := newServer(t)
	req, err := http.NewRequest("GET", s.url+"/admin/v1/whoami", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "kman_session", Value: s.owner})
	req.AddCookie(&http.Cookie{Name: "token", Value: s.owner})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a cookie got status %d, want 401", resp.StatusCode)
	}
}

func TestATokenCreatedOverHTTPWorksUntilRevoked(t *testing.T) {
	s := newServer(t)
	var created admin.CreatedAdminToken
	s.do(t, s.owner, "POST", "/admin/v1/admin-tokens", `{"name":"ci"}`).decode(t, http.StatusCreated, &created)
	if created.Token.Name != "ci" || !strings.HasPrefix(created.Secret, "kma_") {
		t.Fatalf("created %+v", created)
	}
	var who admin.Whoami
	s.do(t, created.Secret, "GET", "/admin/v1/whoami", "").decode(t, http.StatusOK, &who)
	if who.Actor != "ci" {
		t.Errorf("whoami = %q, want ci", who.Actor)
	}
	var revoked admin.AdminToken
	s.do(t, s.owner, "POST", "/admin/v1/admin-tokens/ci/revoke", "").decode(t, http.StatusOK, &revoked)
	if revoked.RevokedAt == nil {
		t.Errorf("revoke returned %+v", revoked)
	}
	if r := s.do(t, created.Secret, "GET", "/admin/v1/whoami", ""); r.status != http.StatusUnauthorized {
		t.Errorf("the revoked token got status %d, want 401", r.status)
	}
}

func TestRotateOverHTTPReturnsTheNewSecret(t *testing.T) {
	s := newServer(t)
	var created, rotated admin.CreatedAdminToken
	s.do(t, s.owner, "POST", "/admin/v1/admin-tokens", `{"name":"ci"}`).decode(t, http.StatusCreated, &created)
	s.do(t, s.owner, "POST", "/admin/v1/admin-tokens/ci/rotate", "").decode(t, http.StatusOK, &rotated)
	if rotated.Secret == created.Secret || rotated.Token.Name != "ci" {
		t.Errorf("rotated %+v", rotated)
	}
	var list admin.AdminTokenList
	s.do(t, rotated.Secret, "GET", "/admin/v1/admin-tokens", "").decode(t, http.StatusOK, &list)
	if len(list.Tokens) != 2 {
		t.Errorf("list = %+v, want ci and owner", list)
	}
}

func TestAdminErrorsMapToStatuses(t *testing.T) {
	s := newServer(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/admin/v1/admin-tokens", `{"name":"Bad Name"}`, 400, "invalid"},
		{"POST", "/admin/v1/admin-tokens", `{"name":"owner"}`, 409, "conflict"},
		{"POST", "/admin/v1/admin-tokens/nope/rotate", "", 404, "not_found"},
		{"POST", "/admin/v1/admin-tokens/owner/revoke", "", 409, "conflict"},
		{"POST", "/admin/v1/admin-tokens", `{"name":"ci","extra":1}`, 400, "invalid"},
		{"POST", "/admin/v1/admin-tokens", `{"name":`, 400, "invalid"},
		{"POST", "/admin/v1/admin-tokens", `{"name":"a"} {"name":"b"}`, 400, "invalid"},
		{"POST", "/admin/v1/admin-tokens", `{"name":"` + strings.Repeat("a", 2<<20) + `"}`, 400, "invalid"},
		{"GET", "/admin/v1/audit?limit=many", "", 400, "invalid"},
		{"GET", "/admin/v1/audit?latest=maybe", "", 400, "invalid"},
		{"GET", "/admin/v1/audit?limit=5000", "", 400, "invalid"},
		{"PATCH", "/admin/v1/settings", `{"spend_timezone":"Mars/Olympus"}`, 400, "invalid"},
		{"GET", "/admin/v1/no-such-thing", "", 404, "not_found"},
	} {
		r := s.do(t, s.owner, tc.method, tc.path, tc.body)
		if r.status != tc.status || r.errorCode(t) != tc.code {
			short := tc.body
			if len(short) > 40 {
				short = short[:40] + "..."
			}
			t.Errorf("%s %s %s: %d %s, want %d %s", tc.method, tc.path, short, r.status, r.body, tc.status, tc.code)
		}
	}
}

func TestResponsesAreNeverCached(t *testing.T) {
	s := newServer(t)
	r := s.do(t, s.owner, "POST", "/admin/v1/admin-tokens", `{"name":"ci"}`)
	if r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q on a response holding a secret, want no-store", r.header.Get("Cache-Control"))
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSettingsAndTheAuditLogOverHTTP(t *testing.T) {
	s := newServer(t)
	var got admin.Settings
	s.do(t, s.owner, "PATCH", "/admin/v1/settings", `{"spend_timezone":"Europe/Berlin","default_key_ttl_seconds":604800}`).
		decode(t, http.StatusOK, &got)
	if got.SpendTimezone != "Europe/Berlin" || got.DefaultKeyTTLSeconds != 604800 || got.MaxKeyLifetimeSeconds != 365*86400 {
		t.Errorf("PATCH settings = %+v", got)
	}
	s.do(t, s.owner, "GET", "/admin/v1/settings", "").decode(t, http.StatusOK, &got)
	if got.SpendTimezone != "Europe/Berlin" {
		t.Errorf("GET settings = %+v", got)
	}
	var page admin.AuditPage
	s.do(t, s.owner, "GET", "/admin/v1/audit?latest=true&limit=2", "").decode(t, http.StatusOK, &page)
	if len(page.Entries) != 2 || !page.More {
		t.Fatalf("latest two = %+v, want two and more", page)
	}
	for _, e := range page.Entries {
		if e.Actor != "owner" || e.Action != "settings.set" || e.ClientIP != "127.0.0.1" {
			t.Errorf("entry %+v, want owner's settings.set from 127.0.0.1", e)
		}
	}
	s.do(t, s.owner, "GET", "/admin/v1/audit?after=1&limit=1", "").decode(t, http.StatusOK, &page)
	if len(page.Entries) != 1 || page.Entries[0].ID != 2 || !page.More {
		t.Errorf("after 1 = %+v, want entry 2 and more", page)
	}
}
