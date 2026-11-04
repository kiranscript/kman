package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// savedToken is the owner token init saved to the credentials file.
func (h *home) savedToken(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.configDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c.Token
}

// login points the CLI at s with the owner token init saved.
func (h *home) login(t *testing.T, s *server) {
	t.Helper()
	stdout, stderr, code := h.runWithInput(t, h.savedToken(t)+"\n", "login", "--admin-url", "http://"+s.admin)
	if code != 0 || !strings.Contains(stdout, "as owner") {
		t.Fatalf("kman login exited %d:\n%s%s", code, stdout, stderr)
	}
}

func TestAnAdminTokenIsCreatedUsedRevokedAndAudited(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)

	out, _ := h.mustRun(t, "admin-token", "create", "ci", "--json")
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || !adminToken.MatchString(created.Secret) {
		t.Fatalf("admin-token create --json printed %q (%v)", out, err)
	}
	ci := h.with("KMAN_ADMIN_TOKEN=" + created.Secret)
	ci.mustRun(t, "settings", "set", "spend_timezone", "Europe/Berlin")
	if got, _ := ci.mustRun(t, "settings", "get", "spend_timezone"); got != "Europe/Berlin\n" {
		t.Errorf("settings get spend_timezone = %q", got)
	}

	h.mustRun(t, "admin-token", "revoke", "ci")
	_, stderr, code := ci.run(t, "admin-token", "list")
	if code != 1 || !strings.Contains(stderr, "revoked") {
		t.Errorf("the revoked token: exit %d, stderr %q; want 1, refused", code, stderr)
	}

	out, _ = h.mustRun(t, "audit", "--json")
	var page struct {
		Entries []struct {
			Actor      string `json:"actor"`
			Action     string `json:"action"`
			TargetKind string `json:"target_kind"`
			TargetID   string `json:"target_id"`
			ClientIP   string `json:"client_ip"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("audit --json: %v\n%s", err, out)
	}
	var got []string
	for _, e := range page.Entries {
		got = append(got, e.Actor+" "+e.Action+" "+e.TargetKind+"/"+e.TargetID)
	}
	want := []string{
		"system:init admin_token.create admin_token/owner",
		"owner admin_token.create admin_token/ci",
		"ci settings.set setting/spend_timezone",
		"owner admin_token.revoke admin_token/ci",
	}
	if !slices.Equal(got, want) {
		t.Errorf("audit log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := len(page.Entries); n > 1 && page.Entries[n-1].ClientIP != "127.0.0.1" {
		t.Errorf("the revoke was recorded from %q, want 127.0.0.1", page.Entries[n-1].ClientIP)
	}

	// The text listing names each actor too.
	text, _ := h.mustRun(t, "audit")
	for _, actor := range []string{"system:init", "owner", "ci"} {
		if !strings.Contains(text, " "+actor+" ") {
			t.Errorf("kman audit does not name %s:\n%s", actor, text)
		}
	}
	if strings.Contains(out+text, created.Secret) {
		t.Error("the audit log shows a secret")
	}
}

func TestTheAdminAPIServesItsOpenAPIDocument(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	if code := get(t, s.admin, "/admin/v1/openapi.json"); code != 200 {
		t.Errorf("GET /admin/v1/openapi.json = %d, want 200", code)
	}
	if code := get(t, s.admin, "/admin/v1/whoami"); code != 401 {
		t.Errorf("GET /admin/v1/whoami with no token = %d, want 401", code)
	}
}

func TestLogoutForgetsTheSavedToken(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.mustRun(t, "logout")
	_, stderr, code := h.run(t, "admin-token", "list")
	if code != 1 || !strings.Contains(stderr, "kman login") {
		t.Errorf("after logout: exit %d, stderr %q; want 1 and a hint to log in", code, stderr)
	}
}
