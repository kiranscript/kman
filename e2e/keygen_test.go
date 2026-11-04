package e2e_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// TestKeygenPrintsAFreshMasterKeyAndStoresNothing runs keygen with every
// home and config location pointed at empty directories and no network
// settings, then checks it printed only a key and wrote nothing anywhere.
func TestKeygenPrintsAFreshMasterKeyAndStoresNothing(t *testing.T) {
	root := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH")}
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA", "USERPROFILE", "TMPDIR", "TEMP", "TMP"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		env = append(env, name+"="+dir)
	}
	if v := os.Getenv("SYSTEMROOT"); v != "" {
		env = append(env, "SYSTEMROOT="+v) // Windows needs it to start a process
	}
	// No proxy is reachable, so a network call would fail.
	env = append(env, "HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1")

	keygen := func() []byte {
		cmd := exec.Command(kman, "keygen")
		cmd.Dir = root
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("kman keygen: %v\n%s", err, stderr.Bytes())
		}
		if stderr.Len() != 0 {
			t.Errorf("kman keygen wrote to stderr: %q", stderr.Bytes())
		}
		return stdout.Bytes()
	}
	a, b := keygen(), keygen()

	if !regexp.MustCompile(`^[A-Za-z0-9+/]{43}=\n$`).Match(a) {
		t.Fatalf("kman keygen printed %q, want one line of base64", a)
	}
	key, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(a)))
	if err != nil || len(key) != 32 {
		t.Fatalf("the printed key decodes to %d bytes (%v), want 32", len(key), err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two runs of kman keygen printed the same key")
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("kman keygen wrote %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
