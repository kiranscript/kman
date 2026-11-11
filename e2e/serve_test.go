package e2e_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// installed runs kman init in a fresh home, then points both listeners at
// free loopback ports so tests can run side by side.
func installed(t *testing.T) *home {
	t.Helper()
	notInContainer(t)
	h := newHome(t)
	h.mustRun(t, "init")
	h.editConfig(t, "proxy_listen: 127.0.0.1:8080", "proxy_listen: 127.0.0.1:0")
	h.editConfig(t, "admin_listen: 127.0.0.1:8081", "admin_listen: 127.0.0.1:0")
	return h
}

// editConfig replaces old with new in kman.yaml.
func (h *home) editConfig(t *testing.T, old, new string) {
	t.Helper()
	path := filepath.Join(h.configDir(), "kman.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), old) {
		t.Fatalf("kman.yaml lacks %q:\n%s", old, raw)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// appendConfig adds lines to kman.yaml.
func (h *home) appendConfig(t *testing.T, lines string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(h.configDir(), "kman.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(lines); err != nil {
		t.Fatal(err)
	}
}

// server is a running kman serve.
type server struct {
	cmd     *exec.Cmd
	proxy   string // host:port
	admin   string // host:port, or unix:<path>
	logs    *logs
	exited  chan struct{}
	exitErr error
}

// logs is what kman serve wrote to stderr, safe to read while it runs.
type logs struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logs) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.WriteString(line + "\n")
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var listening = regexp.MustCompile(`msg="(proxy|admin) listening" addr=(\S+)`)

// serve starts kman serve and waits until both listeners are up.
func (h *home) serve(t *testing.T, args ...string) *server {
	t.Helper()
	cmd := h.command(append([]string{"serve"}, args...)...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &server{cmd: cmd, logs: &logs{}, exited: make(chan struct{})}
	up := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			s.logs.add(line)
			if m := listening.FindStringSubmatch(line); m != nil {
				if m[1] == "proxy" {
					s.proxy = m[2]
				} else {
					s.admin = m[2]
				}
				if s.proxy != "" && s.admin != "" {
					close(up)
				}
			}
		}
		io.Copy(io.Discard, stderr)
		s.exitErr = cmd.Wait()
		close(s.exited)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-s.exited
	})
	select {
	case <-up:
	case <-s.exited:
		t.Fatalf("kman serve exited: %v\n%s", s.exitErr, s.logs)
	case <-time.After(30 * time.Second):
		t.Fatalf("kman serve did not start listening:\n%s", s.logs)
	}
	return s
}

// wait waits for kman serve to exit and returns its exit code.
func (s *server) wait(t *testing.T) int {
	t.Helper()
	select {
	case <-s.exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("kman serve did not exit:\n%s", s.logs)
	}
	if ee, ok := s.exitErr.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	if s.exitErr != nil {
		t.Fatal(s.exitErr)
	}
	return 0
}

// get makes a request to a listener and returns the status code.
func get(t *testing.T, addr, path string) int {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://" + addr + path
	if sock, ok := strings.CutPrefix(addr, "unix:"); ok {
		client.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}
		url = "http://kman" + path
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// getTLS makes a request over TLS, trusting only the certificate in
// certFile, and returns the status code.
func getTLS(t *testing.T, addr, certFile string) int {
	t.Helper()
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemBytes)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("GET https://%s/: %v", addr, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestServeBindsBothListenersToLoopback(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	for name, addr := range map[string]string{"proxy": s.proxy, "admin": s.admin} {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || host != "127.0.0.1" {
			t.Errorf("the %s listener is on %q, want 127.0.0.1", name, addr)
		}
		if code := get(t, addr, "/"); code == 0 {
			t.Errorf("the %s listener did not answer", name)
		}
	}
}

func TestServeShutsDownGracefullyOnSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot send SIGINT or SIGTERM to another process")
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			h := installed(t)
			s := h.serve(t)
			if err := s.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			if code := s.wait(t); code != 0 {
				t.Fatalf("kman serve exited %d after %v, want 0:\n%s", code, sig, s.logs)
			}
			if !strings.Contains(s.logs.String(), "stopped") {
				t.Errorf("kman serve did not log that it stopped:\n%s", s.logs)
			}
			// The store was closed cleanly: SQLite checkpoints and removes the
			// WAL as the last connection closes.
			if _, err := os.Stat(filepath.Join(h.dataDir(), "kman.db-wal")); err == nil {
				t.Error("the WAL is still there: the store was not closed")
			}
			if _, err := net.DialTimeout("tcp", s.proxy, time.Second); err == nil {
				t.Error("the proxy still accepts connections after shutdown")
			}
		})
	}
}
