package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// home is an empty user environment for one run of kman: every home and
// config location points into a fresh directory, and no network is
// reachable.
type home struct {
	root string
	env  []string
}

func newHome(t *testing.T) *home {
	t.Helper()
	h := &home{root: t.TempDir(), env: []string{"PATH=" + os.Getenv("PATH")}}
	for _, name := range []string{"HOME", "APPDATA", "LOCALAPPDATA", "USERPROFILE", "TMPDIR", "TEMP", "TMP"} {
		dir := filepath.Join(h.root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		h.env = append(h.env, name+"="+dir)
	}
	if v := os.Getenv("SYSTEMROOT"); v != "" {
		h.env = append(h.env, "SYSTEMROOT="+v) // Windows needs it to start a process
	}
	h.env = append(h.env, "HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1")
	return h
}

// notInContainer skips a test that expects kman to run outside a container
// when the tests themselves run in one.
func notInContainer(t *testing.T) {
	t.Helper()
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			t.Skip("the tests run in a container")
		}
	}
}

// with returns a copy of h whose environment also sets each "NAME=value".
func (h *home) with(vars ...string) *home {
	return &home{root: h.root, env: append(append([]string(nil), h.env...), vars...)}
}

// configDir and dataDir are where kman keeps its files by default.
func (h *home) configDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(h.root, "APPDATA", "kman")
	}
	return filepath.Join(h.root, "HOME", ".config", "kman")
}

func (h *home) dataDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(h.root, "LOCALAPPDATA", "kman")
	}
	return filepath.Join(h.root, "HOME", ".local", "share", "kman")
}

// command is kman with args in this home.
func (h *home) command(args ...string) *exec.Cmd {
	cmd := exec.Command(kman, args...)
	cmd.Dir = h.root
	cmd.Env = h.env
	return cmd
}

// run runs kman to the end and returns its output and exit code. A run
// that has not ended after 30 s is killed and fails the test.
func (h *home) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return h.runWithInput(t, "", args...)
}

// runWithInput is run with stdin reading input.
func (h *home) runWithInput(t *testing.T, input string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := h.command(args...)
	cmd.Stdin = strings.NewReader(input)
	timer := time.AfterFunc(30*time.Second, func() { cmd.Process.Kill() })
	defer func() {
		if !timer.Stop() {
			t.Fatalf("kman %v ran over 30 s and was killed:\n%s", args, stderr)
		}
	}()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), errb.String(), ee.ExitCode()
	}
	if err != nil {
		t.Fatalf("kman %v: %v", args, err)
	}
	return out.String(), errb.String(), 0
}

// mustRun runs kman and fails the test unless it exits 0.
func (h *home) mustRun(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	stdout, stderr, code := h.run(t, args...)
	if code != 0 {
		t.Fatalf("kman %v exited %d\nstdout:\n%s\nstderr:\n%s", args, code, stdout, stderr)
	}
	return stdout, stderr
}

// mode checks path's permission bits, where the OS has them.
func mode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != want {
		t.Errorf("%s has mode %v, want %v", path, fi.Mode().Perm(), want)
	}
}
