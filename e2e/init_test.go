package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var adminToken = regexp.MustCompile(`\bkma_[0-9A-Za-z]{49}\b`)

func TestInitSetsUpAnInstallWithoutAskingAnything(t *testing.T) {
	notInContainer(t)
	h := newHome(t)
	stdout, stderr := h.mustRun(t, "init")

	cfg, key := filepath.Join(h.configDir(), "kman.yaml"), filepath.Join(h.configDir(), "master.key")
	mode(t, cfg, 0o600)
	mode(t, key, 0o600)
	mode(t, h.dataDir(), 0o700)
	if _, err := os.Stat(filepath.Join(h.dataDir(), "kman.db")); err != nil {
		t.Errorf("init created no database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dataDir(), "master.key")); err == nil {
		t.Error("init put the master key in the data directory")
	}

	tokens := adminToken.FindAllString(stdout+stderr, -1)
	if len(tokens) != 1 {
		t.Fatalf("init printed %d admin tokens, want the owner token once:\n%s%s", len(tokens), stdout, stderr)
	}
	creds := filepath.Join(h.configDir(), "credentials.json")
	mode(t, creds, 0o600)
	raw, err := os.ReadFile(creds)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		AdminURL string `json:"admin_url"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("credentials file: %v\n%s", err, raw)
	}
	if saved.Token != tokens[0] || saved.AdminURL != "http://127.0.0.1:8081" {
		t.Errorf("credentials file = %+v, want the printed token and the default admin URL", saved)
	}

	for _, next := range []string{"kman serve", "kman upstream add openai --preset openai", "kman key create my-app --upstream openai"} {
		if !strings.Contains(stdout, next) {
			t.Errorf("init did not print the next command %q:\n%s", next, stdout)
		}
	}

	yaml, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"proxy_listen: 127.0.0.1:8080", "admin_listen: 127.0.0.1:8081", "key_source: file"} {
		if !strings.Contains(string(yaml), line) {
			t.Errorf("kman.yaml lacks %q:\n%s", line, yaml)
		}
	}
	if strings.Contains(string(yaml), tokens[0]) {
		t.Error("kman.yaml holds the admin token")
	}
}
