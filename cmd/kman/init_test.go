package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for a command and a test to share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// testSys is a sys with fake output, an environment of its own and a
// terminal that answers prompts from answers, in turn.
func testSys(t *testing.T, env map[string]string, answers ...string) (*sys, *syncBuffer, *syncBuffer) {
	t.Helper()
	var stdout, stderr syncBuffer
	var mu sync.Mutex
	return &sys{
		stdout: &stdout, stderr: &stderr,
		getenv:      func(k string) string { return env[k] },
		now:         func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) },
		goos:        "linux",
		inContainer: func() bool { return false },
		passphrase: func(string) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			if len(answers) == 0 {
				return nil, errors.New("no terminal")
			}
			a := answers[0]
			answers = answers[1:]
			return []byte(a), nil
		},
	}, &stdout, &stderr
}

func TestInitWithAPassphraseSealsTheKeyAndServeAsksForIt(t *testing.T) {
	dir := t.TempDir()
	config, data := filepath.Join(dir, "config"), filepath.Join(dir, "data")
	s, stdout, stderr := testSys(t, nil, "correct horse", "correct horse")
	if code := run(t.Context(), []string{"init", "--config-dir", config, "--data-dir", data, "--key-source", "passphrase", "--allow-debug"}, s); code != 0 {
		t.Fatalf("init exited %d:\n%s%s", code, stdout, stderr)
	}
	sealed, err := os.ReadFile(filepath.Join(config, sealedKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(sealed, []byte("kman-sealed-master-key")) {
		t.Errorf("%s is not a sealed key file:\n%s", sealedKeyFile, sealed)
	}
	if _, err := os.Stat(filepath.Join(config, masterKeyFile)); err == nil {
		t.Error("init also wrote a plain master key")
	}
	freePorts(t, config)

	// The right passphrase starts serve.
	s, _, stderr = testSys(t, nil, "correct horse")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int)
	go func() { done <- run(ctx, []string{"serve", "--config-dir", config, "--allow-debug"}, s) }()
	waitFor(t, stderr, "kman is running", done)
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("serve exited %d:\n%s", code, stderr)
	}

	// A wrong one does not.
	s, _, stderr = testSys(t, nil, "wrong horse")
	if code := run(t.Context(), []string{"serve", "--config-dir", config, "--allow-debug"}, s); code != 1 || !strings.Contains(stderr.String(), "passphrase") {
		t.Fatalf("serve with the wrong passphrase exited %d:\n%s", code, stderr)
	}
}

func TestInitRefusesPassphrasesThatDiffer(t *testing.T) {
	dir := t.TempDir()
	config, data := filepath.Join(dir, "config"), filepath.Join(dir, "data")
	s, _, stderr := testSys(t, nil, "correct horse", "correct hose")
	if code := run(t.Context(), []string{"init", "--config-dir", config, "--data-dir", data, "--key-source", "passphrase", "--allow-debug"}, s); code != 1 {
		t.Fatalf("init exited %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "differ") {
		t.Errorf("init did not say the passphrases differ:\n%s", stderr)
	}
	// It rolled back, so it can run again.
	for _, p := range []string{config, data} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("a failed init left %s behind", p)
		}
	}
}

// freePorts points both listeners in config/kman.yaml at free ports.
func freePorts(t *testing.T, config string) {
	t.Helper()
	path := filepath.Join(config, configFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.NewReplacer(defaultProxyListen, "127.0.0.1:0", defaultAdminListen, "127.0.0.1:0").Replace(string(raw))
	if err := os.WriteFile(path, []byte(out), fileMode); err != nil {
		t.Fatal(err)
	}
}

// waitFor waits until w holds want, failing if the command ends first.
func waitFor(t *testing.T, w *syncBuffer, want string, done <-chan int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(w.String(), want) {
		select {
		case code := <-done:
			t.Fatalf("exited %d before %q:\n%s", code, want, w)
		case <-deadline:
			t.Fatalf("no %q after 10 s:\n%s", want, w)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
