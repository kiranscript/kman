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

// keygen writes a fresh master key to a file outside the home's kman
// directories and returns its path.
func (h *home) keygen(t *testing.T, name string) string {
	t.Helper()
	key, _ := h.mustRun(t, "keygen")
	path := filepath.Join(h.root, name)
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitInAContainerWithNoKeySourceRefusesAndPrintsTheDockerSteps(t *testing.T) {
	for _, args := range [][]string{{"init"}, {"init", "--key-source", "file"}, {"init", "--key-source", "passphrase"}, {"init", "--key-source", "env"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := newHome(t).with("container=docker")
			stdout, stderr, code := h.run(t, args...)
			if code != 1 {
				t.Fatalf("kman init in a container exited %d, want 1:\n%s%s", code, stdout, stderr)
			}
			for _, step := range []string{"keygen > master.key", "KMAN_MASTER_KEY_FILE", "docker compose run --rm kman init", "docker compose up -d"} {
				if !strings.Contains(stderr, step) {
					t.Errorf("the refusal lacks the Docker step %q:\n%s", step, stderr)
				}
			}
			if adminToken.MatchString(stdout + stderr) {
				t.Error("init printed a token although it refused")
			}
			nothingWritten(t, h)
		})
	}
}

// nothingWritten checks that kman left no file in the home.
func nothingWritten(t *testing.T, h *home) {
	t.Helper()
	err := filepath.WalkDir(h.root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("kman wrote %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInitInAContainerUsesTheMountedKeyAndNeverSavesTheToken(t *testing.T) {
	h := newHome(t)
	key := h.keygen(t, "secret.key")
	h = h.with("container=docker", "KMAN_MASTER_KEY_FILE="+key)
	stdout, _ := h.mustRun(t, "init")
	if len(adminToken.FindAllString(stdout, -1)) != 1 {
		t.Errorf("init did not print the owner token once:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(h.configDir(), "credentials.json")); err == nil {
		t.Error("init saved the token in a container")
	}
	if _, err := os.Stat(filepath.Join(h.configDir(), "master.key")); err == nil {
		t.Error("init made a master key although one was mounted")
	}
	yaml, err := os.ReadFile(filepath.Join(h.configDir(), "kman.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yaml), "master_key_file: "+key) {
		t.Errorf("kman.yaml does not name the mounted key:\n%s", yaml)
	}
}

func TestInitWithNoSaveTokenPrintsButDoesNotSave(t *testing.T) {
	notInContainer(t)
	h := newHome(t)
	stdout, _ := h.mustRun(t, "init", "--no-save-token")
	if len(adminToken.FindAllString(stdout, -1)) != 1 {
		t.Errorf("init did not print the owner token once:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(h.configDir(), "credentials.json")); err == nil {
		t.Error("init saved the token despite --no-save-token")
	}
}

func TestInitTakesTheDataDirectoryFromAFlag(t *testing.T) {
	notInContainer(t)
	h := newHome(t)
	data := filepath.Join(h.root, "elsewhere", "data")
	h.mustRun(t, "init", "--data-dir", data)
	mode(t, data, 0o700)
	if _, err := os.Stat(filepath.Join(data, "kman.db")); err != nil {
		t.Errorf("no database in --data-dir: %v", err)
	}
	if _, err := os.Stat(h.dataDir()); err == nil {
		t.Error("init also created the default data directory")
	}
	h.editConfig(t, "proxy_listen: 127.0.0.1:8080", "proxy_listen: 127.0.0.1:0")
	h.editConfig(t, "admin_listen: 127.0.0.1:8081", "admin_listen: 127.0.0.1:0")
	h.serve(t)
}

func TestInitRefusesADataDirectoryHoldingTheMasterKey(t *testing.T) {
	notInContainer(t)
	h := newHome(t)
	_, stderr, code := h.run(t, "init", "--data-dir", h.configDir())
	if code != 1 || !strings.Contains(stderr, "inside the data directory") {
		t.Fatalf("init with the data directory on the config directory exited %d:\n%s", code, stderr)
	}
	nothingWritten(t, h)
}

func TestInitRefusesToRunTwice(t *testing.T) {
	notInContainer(t)
	h := newHome(t)
	h.mustRun(t, "init")
	before, err := os.ReadFile(filepath.Join(h.configDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, code := h.run(t, "init")
	if code != 1 || !strings.Contains(stderr, "already") {
		t.Fatalf("a second init exited %d:\n%s", code, stderr)
	}
	after, err := os.ReadFile(filepath.Join(h.configDir(), "master.key"))
	if err != nil || string(after) != string(before) {
		t.Fatal("a second init changed the master key")
	}
}

func TestEachKeySourceInitsAndServes(t *testing.T) {
	notInContainer(t)
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, h *home) *home
		args  []string
		want  string // in kman.yaml
	}{
		{"key file from the environment", func(t *testing.T, h *home) *home {
			return h.with("KMAN_MASTER_KEY_FILE=" + h.keygen(t, "mounted.key"))
		}, nil, "key_source: file"},
		{"env", func(t *testing.T, h *home) *home {
			key, _ := h.mustRun(t, "keygen")
			return h.with("KMAN_MASTER_KEY=" + strings.TrimSpace(key))
		}, []string{"--key-source", "env"}, "key_source: env"},
		{"systemd-creds", func(t *testing.T, h *home) *home {
			creds := filepath.Join(h.root, "creds")
			if err := os.Mkdir(creds, 0o700); err != nil {
				t.Fatal(err)
			}
			key, _ := h.mustRun(t, "keygen")
			if err := os.WriteFile(filepath.Join(creds, "master.key"), []byte(key), 0o600); err != nil {
				t.Fatal(err)
			}
			return h.with("CREDENTIALS_DIRECTORY=" + creds)
		}, []string{"--key-source", "systemd-creds"}, "key_source: systemd-creds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.setup(t, newHome(t))
			h.mustRun(t, append([]string{"init"}, tc.args...)...)
			yaml, err := os.ReadFile(filepath.Join(h.configDir(), "kman.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(yaml), tc.want) {
				t.Errorf("kman.yaml lacks %q:\n%s", tc.want, yaml)
			}
			if _, err := os.Stat(filepath.Join(h.configDir(), "master.key")); err == nil {
				t.Error("init made a master key file although the key came from elsewhere")
			}
			h.editConfig(t, "proxy_listen: 127.0.0.1:8080", "proxy_listen: 127.0.0.1:0")
			h.editConfig(t, "admin_listen: 127.0.0.1:8081", "admin_listen: 127.0.0.1:0")
			h.serve(t)
		})
	}
}

func TestServeRefusesAnotherMasterKey(t *testing.T) {
	h := installed(t)
	other := h.keygen(t, "other.key")
	h.with("KMAN_MASTER_KEY_FILE="+other).refused(t, "does not unwrap the data key")
}
