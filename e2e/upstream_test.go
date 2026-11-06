package e2e_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const sentinel = "sk-live-e2e-sentinel-0123456789abcdefWXYZ"

// upstreamShown is what upstream show --json says, as far as these tests look.
type upstreamShown struct {
	Name          string
	Preset        string
	PresetVersion int `json:"preset_version"`
	Host          string
	Credentials   []struct{ ID, Name, Last4 string }
}

func (h *home) showUpstream(t *testing.T, name string) upstreamShown {
	t.Helper()
	out, _ := h.mustRun(t, "upstream", "show", name, "--json")
	var u upstreamShown
	if err := json.Unmarshal([]byte(out), &u); err != nil {
		t.Fatalf("upstream show --json printed %q: %v", out, err)
	}
	return u
}

// addOpenAI adds the openai preset with the sentinel as its credential.
func (h *home) addOpenAI(t *testing.T) string {
	t.Helper()
	stdout, stderr, code := h.runWithInput(t, sentinel+"\n", "upstream", "add", "openai", "--preset", "openai")
	if code != 0 {
		t.Fatalf("upstream add exited %d:\n%s%s", code, stdout, stderr)
	}
	return stdout + stderr
}

func TestAnUpstreamFromAPresetSurvivesARestart(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)

	said := h.addOpenAI(t)
	if !strings.Contains(said, "api.openai.com") {
		t.Errorf("upstream add said:\n%s", said)
	}
	u := h.showUpstream(t, "openai")
	if u.Preset != "openai" || u.PresetVersion != 1 || u.Host != "api.openai.com" || len(u.Credentials) != 1 || u.Credentials[0].Last4 != "WXYZ" {
		t.Errorf("upstream show = %+v", u)
	}

	stopServer(t, s)
	restarted := h.serve(t)
	h.login(t, restarted)
	if got := h.showUpstream(t, "openai"); got.Host != u.Host || len(got.Credentials) != 1 || got.Credentials[0].ID != u.Credentials[0].ID {
		t.Errorf("after a restart, upstream show = %+v", got)
	}
}

func TestACredentialReachesNoOutputOfTheRealBinary(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)

	said := h.addOpenAI(t)
	for _, args := range [][]string{
		{"upstream", "list"},
		{"upstream", "list", "--json"},
		{"upstream", "show", "openai"},
		{"upstream", "show", "openai", "--json"},
		{"audit", "--json"},
		{"audit", "export"},
	} {
		out, errOut := h.mustRun(t, args...)
		said += out + errOut
	}
	stdout, stderr, _ := h.runWithInput(t, sentinel+"\n", "credential", "add", "Bad Name", "--upstream", "openai")
	said += stdout + stderr
	stopServer(t, s)

	if strings.Contains(said, sentinel) {
		t.Errorf("the CLI printed the credential:\n%s", said)
	}
	if strings.Contains(s.logs.String(), sentinel) {
		t.Errorf("the server logged the credential:\n%s", s.logs)
	}
	files, err := filepath.Glob(filepath.Join(h.dataDir(), "kman.db*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no database files: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), sentinel) {
			t.Errorf("%s holds the credential in the clear", f)
		}
	}
}

func TestServeRefusesACredentialThatDoesNotDecrypt(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addOpenAI(t)
	id := h.showUpstream(t, "openai").Credentials[0].ID
	stopServer(t, s)

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(h.dataDir(), "kman.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE credentials SET ciphertext = x'00112233' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	h.refused(t, id)
}

// stopServer stops the server the way an operator would, or kills it where
// one process cannot signal another. Either way its data is durable.
func stopServer(t *testing.T, s *server) {
	t.Helper()
	var err error
	if runtime.GOOS == "windows" {
		err = s.cmd.Process.Kill()
	} else {
		err = s.cmd.Process.Signal(os.Interrupt)
	}
	if err != nil {
		t.Fatal(err)
	}
	s.wait(t)
}
