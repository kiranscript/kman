package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/token"
)

// kman scan needs no install, no server and no network: this home has none, and
// its proxy variables point at nothing.
func TestScanFindsAKeyOnAMachineThatNeverRanKman(t *testing.T) {
	notInContainer(t)
	h := newHome(t)
	id := token.NewKeyID()
	minted, err := token.MintVirtualKey(id)
	if err != nil {
		t.Fatal(err)
	}
	secret := minted.Reveal()
	repo := filepath.Join(h.root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "deploy", ".env"), []byte("OPENAI_API_KEY="+secret+"\n"), 0o600)
	os.WriteFile(filepath.Join(repo, "notes.md"), []byte("kmn_Ab3dE6gH_"+strings.Repeat("x", 49)+" is not a key: its checksum is wrong\n"), 0o600)

	stdout, stderr, code := h.run(t, "scan", repo)
	if code != 3 {
		t.Fatalf("kman scan exited %d, want 3:\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, filepath.Join(repo, "deploy", ".env")+":1:16") || !strings.Contains(stdout, "kman key show "+string(id)) {
		t.Errorf("output does not say where the key is and which key it is:\n%s", stdout)
	}
	if strings.Contains(stdout+stderr, secret[len("kmn_")+9:][:16]) {
		t.Errorf("scan printed the secret:\n%s%s", stdout, stderr)
	}
	if strings.Contains(stdout, "notes.md") {
		t.Errorf("scan reported a lookalike whose checksum is wrong:\n%s", stdout)
	}

	out, _, code := h.run(t, "scan", "--json", repo)
	var report struct {
		Scanned  int `json:"scanned"`
		Findings []struct {
			KeyID    string `json:"key_id"`
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil || code != 3 || report.Scanned != 2 || len(report.Findings) != 1 ||
		report.Findings[0].KeyID != string(id) || report.Findings[0].Severity != "high" {
		t.Errorf("scan --json: exit %d, %v, %s", code, err, out)
	}

	clean := filepath.Join(h.root, "clean")
	os.Mkdir(clean, 0o755)
	os.WriteFile(filepath.Join(clean, "a"), []byte("nothing\n"), 0o600)
	if stdout, _, code := h.run(t, "scan", clean); code != 0 || !strings.Contains(stdout, "No kman keys found") {
		t.Errorf("a clean tree: exit %d, %s", code, stdout)
	}
}

func TestScanNamesTheRealKeyAndRevokingItEndsIt(t *testing.T) {
	p := behindKman(t, "generic-json")
	id := p.h.showKey(t, "my-app").ID
	file := filepath.Join(p.h.root, "leaked.log")
	os.WriteFile(file, []byte("Authorization: Bearer "+p.secret+"\n"), 0o600)

	stdout, _, code := p.h.run(t, "scan", file)
	if code != 3 || !strings.Contains(stdout, "kman key show "+id) {
		t.Fatalf("scan: exit %d:\n%s", code, stdout)
	}
	// The id the scan printed is the one to act on.
	p.h.mustRun(t, "key", "revoke", id)
	resp, body := p.send("POST", "/v1/things", strings.NewReader("{}"))
	wantError(t, resp, body, 401, "key_revoked")
}

func TestExposureListsAnOpenKeyAndStrictFailsUntilItIsLocked(t *testing.T) {
	p := behindKman(t, "generic-json")
	stdout, stderr, code := p.h.run(t, "exposure")
	if code != 0 || !strings.Contains(stdout, "my-app") || !strings.Contains(stdout, "open to every address") {
		t.Fatalf("exposure: exit %d:\n%s%s", code, stdout, stderr)
	}
	if _, _, code := p.h.run(t, "exposure", "--strict"); code != 3 {
		t.Errorf("exposure --strict with an open key exited %d, want 3", code)
	}
	p.h.mustRun(t, "key", "update", "my-app", "--policy", `{"allowed_cidrs":["127.0.0.0/8"]}`)
	if stdout, _, code := p.h.run(t, "exposure", "--strict"); code != 0 || !strings.Contains(stdout, "No exposed keys") {
		t.Errorf("after locking the key: exit %d:\n%s", code, stdout)
	}
	out, _ := p.h.mustRun(t, "exposure", "--json")
	if !strings.Contains(out, `"findings": []`) {
		t.Errorf("exposure --json = %s", out)
	}
}
