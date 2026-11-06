package e2e_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// keyShown is what key show --json says, as far as these tests look.
type keyShown struct {
	ID, Name, Upstream, Note string
	ExpiresAt                string `json:"expires_at"`
}

func (h *home) showKey(t *testing.T, ref string) keyShown {
	t.Helper()
	out, _ := h.mustRun(t, "key", "show", ref, "--json")
	var k keyShown
	if err := json.Unmarshal([]byte(out), &k); err != nil {
		t.Fatalf("key show --json printed %q: %v", out, err)
	}
	return k
}

var keySecret = regexp.MustCompile(`kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}`)

// plain is plain kman's output, which exits 0 at every stage.
func (h *home) plain(t *testing.T) string {
	t.Helper()
	out, _ := h.mustRun(t)
	return out
}

func wantStage(t *testing.T, out, next string) {
	t.Helper()
	if !strings.Contains(out, "Next: "+next+"\n") {
		t.Errorf("want Next: %s, in plain kman's:\n%s", next, out)
	}
}

func TestPlainKmanNamesTheNextCommandFromNothingToReady(t *testing.T) {
	notInContainer(t)
	h := newHome(t)
	wantStage(t, h.plain(t), "kman init")

	h.mustRun(t, "init")
	h.editConfig(t, "proxy_listen: 127.0.0.1:8080", "proxy_listen: 127.0.0.1:0")
	h.editConfig(t, "admin_listen: 127.0.0.1:8081", "admin_listen: 127.0.0.1:0")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nothing := "KMAN_ADMIN_URL=http://" + l.Addr().String()
	l.Close()
	wantStage(t, h.with(nothing).plain(t), "kman serve")

	s := h.serve(t)
	h.login(t, s)
	wantStage(t, h.plain(t), "kman upstream add openai --preset openai")

	h.addOpenAI(t)
	wantStage(t, h.plain(t), "kman key create my-app --upstream openai")

	h.mustRun(t, "key", "create", "my-app", "--upstream", "openai")
	out := h.plain(t)
	wantStage(t, out, "kman key list")
	if !strings.Contains(out, "ready") || !strings.Contains(out, "Price data generated ") || !strings.Contains(out, "usage: kman") {
		t.Errorf("plain kman at ready:\n%s", out)
	}

	stopServer(t, s)
	wantStage(t, h.plain(t), "kman serve")
}

func TestAKeySurvivesARestartAndItsSecretIsShownOnce(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addOpenAI(t)

	created, _ := h.mustRun(t, "key", "create", "my-app", "--upstream", "openai", "--ttl", "30d", "--note", "billing")
	secret := keySecret.FindString(created)
	if secret == "" || !strings.Contains(created, "export OPENAI_API_KEY="+secret) {
		t.Fatalf("key create printed:\n%s", created)
	}
	k := h.showKey(t, "my-app")
	if k.Name != "my-app" || k.Upstream != "openai" || k.Note != "billing" {
		t.Errorf("key show = %+v", k)
	}
	if got := h.showKey(t, k.ID); got != k {
		t.Errorf("key show by id = %+v, by name %+v", got, k)
	}

	h.mustRun(t, "key", "update", "my-app", "--note", "moved")
	var seen strings.Builder
	for _, args := range [][]string{
		{"key", "list"},
		{"key", "list", "--json"},
		{"key", "show", "my-app"},
		{"key", "show", "my-app", "--json"},
		{"audit", "--json"},
		{"audit", "export"},
		{},
	} {
		stdout, stderr := h.mustRun(t, args...)
		seen.WriteString(stdout + stderr)
	}
	stopServer(t, s)
	seen.WriteString(s.logs.String())
	if strings.Contains(seen.String(), secret) {
		t.Errorf("the secret was shown again:\n%s", seen.String())
	}

	// After a restart, the key is still there, and the database holds no secret.
	restarted := h.serve(t)
	h.login(t, restarted)
	if got := h.showKey(t, "my-app"); got.ID != k.ID || got.Note != "moved" {
		t.Errorf("after a restart, key show = %+v", got)
	}
	stopServer(t, restarted)
	body := secret[strings.LastIndex(secret, "_")+1:][:43] // the secret itself, without the key id or checksum
	files, err := filepath.Glob(filepath.Join(h.dataDir(), "kman.db*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no database files: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), body) {
			t.Errorf("%s holds the secret", f)
		}
	}
}
