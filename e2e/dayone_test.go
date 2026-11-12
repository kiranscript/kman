package e2e_test

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBackupAndRestoreRoundTripThroughTheRealBinary(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addOpenAI(t)
	file := filepath.Join(t.TempDir(), "kman.backup")

	out, _ := h.mustRun(t, "backup", file)
	if !strings.Contains(out, "master key") {
		t.Errorf("backup said %q, want a reminder that the master key is kept separately", out)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(sentinel)) {
		t.Error("the backup holds a credential in the clear")
	}
	if !bytes.HasPrefix(raw, []byte("SQLite format 3\x00")) {
		t.Error("the backup is not a SQLite database")
	}
	mode(t, file, 0o600)
	if _, stderr, code := h.run(t, "backup", file); code != 1 || !strings.Contains(stderr, "exists") {
		t.Errorf("a second backup to the same file exited %d: %s", code, stderr)
	}

	// Something made after the backup, which a restore must take away.
	h.mustRun(t, "admin-token", "create", "after-the-backup")

	// The server is still running: restore refuses it.
	if _, stderr, code := h.run(t, "restore", file); code != 1 || !strings.Contains(stderr, "stop kman serve") {
		t.Fatalf("restore beside a running server exited %d: %s", code, stderr)
	}
	stopServer(t, s)
	h.mustRun(t, "restore", file)

	restarted := h.serve(t)
	h.login(t, restarted)
	if got := h.showUpstream(t, "openai"); len(got.Credentials) != 1 || got.Credentials[0].Last4 != "WXYZ" {
		t.Errorf("after the restore, upstream show = %+v", got)
	}
	tokens, _ := h.mustRun(t, "admin-token", "list")
	if strings.Contains(tokens, "after-the-backup") {
		t.Errorf("the token made after the backup survived the restore:\n%s", tokens)
	}
	audit, _ := h.mustRun(t, "audit")
	for _, want := range []string{"system:restore", "data.restore"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit lacks %q:\n%s", want, audit)
		}
	}
}

func TestRestoreWithTheWrongMasterKeyChangesNothing(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addOpenAI(t)
	file := filepath.Join(t.TempDir(), "kman.backup")
	h.mustRun(t, "backup", file)
	stopServer(t, s)
	// A rekey after the backup: the backup needs the key from before it.
	h.mustRun(t, "rekey")
	_, stderr, code := h.run(t, "restore", file)
	if code != 1 || !strings.Contains(stderr, "does not open this backup") {
		t.Fatalf("restore with the new key exited %d: %s", code, stderr)
	}
	restarted := h.serve(t) // the install is as the rekey left it
	h.login(t, restarted)
	if got := h.showUpstream(t, "openai"); len(got.Credentials) != 1 {
		t.Errorf("after the refusal, upstream show = %+v", got)
	}
}

func TestPruneThroughTheRealBinaryIsAudited(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	out, _ := h.mustRun(t, "prune", "--before", "2020-01-01")
	if !strings.Contains(out, "0 events deleted") {
		t.Errorf("prune said %q", out)
	}
	h.mustRun(t, "prune", "--before", "2020-01-01", "--audit-log")
	audit, _ := h.mustRun(t, "audit")
	if strings.Count(audit, "data.prune") != 2 {
		t.Errorf("audit should hold both prunes, and they are kept:\n%s", audit)
	}
	if _, stderr, code := h.run(t, "prune", "--before", "2999-01-01"); code != 1 || !strings.Contains(stderr, "future") {
		t.Errorf("a prune to the future exited %d: %s", code, stderr)
	}
}

// proxyRecorder is a listener standing in for the operator's HTTPS proxy: what
// kman reaches for through HTTPS_PROXY arrives here, as the CONNECT line naming
// where it was going, and goes no further. Nothing leaves the machine.
type proxyRecorder struct {
	addr string
	mu   sync.Mutex
	got  []string
}

func newProxyRecorder(t *testing.T) *proxyRecorder {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	p := &proxyRecorder{addr: l.Addr().String()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetReadDeadline(time.Now().Add(5 * time.Second))
				line, _ := bufio.NewReader(c).ReadString('\n')
				p.mu.Lock()
				p.got = append(p.got, strings.TrimSpace(line))
				p.mu.Unlock()
				c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
			}()
		}
	}()
	return p
}

func (p *proxyRecorder) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.got...)
}

func (p *proxyRecorder) env() []string {
	u := "http://" + p.addr
	return []string{"HTTPS_PROXY=" + u, "HTTP_PROXY=" + u, "ALL_PROXY=" + u, "https_proxy=" + u, "http_proxy=" + u, "NO_PROXY="}
}

// Nothing in kman contacts GitHub unless the operator runs version --check or
// turns update_check on (ADR 0008).
func TestNothingContactsGitHubUnlessTheOperatorRunsTheCheckOrOptsIn(t *testing.T) {
	p := newProxyRecorder(t)
	h := installed(t).with(p.env()...)
	s := h.serve(t)
	h.login(t, s)
	file := filepath.Join(t.TempDir(), "kman.backup")
	h.mustRun(t, "version")
	h.mustRun(t)
	h.mustRun(t, "backup", file)
	h.mustRun(t, "prune", "--before", "2020-01-01")
	h.mustRun(t, "audit")
	time.Sleep(300 * time.Millisecond)
	if got := p.calls(); len(got) != 0 {
		t.Fatalf("kman reached out without being asked: %v", got)
	}

	// The operator runs the check: that, and only that, asks GitHub.
	_, stderr, code := h.run(t, "version", "--check")
	if code != 1 {
		t.Errorf("version --check through a proxy that refuses exited %d, want 1: %s", code, stderr)
	}
	got := p.calls()
	if len(got) != 1 || !strings.HasPrefix(got[0], "CONNECT api.github.com:443") {
		t.Errorf("version --check reached %v, want exactly api.github.com:443", got)
	}
}

func TestUpdateCheckTrueMakesServeAskGitHubAtStartupAndNothingElse(t *testing.T) {
	p := newProxyRecorder(t)
	h := installed(t).with(p.env()...)
	h.appendConfig(t, "update_check: true\n")
	s := h.serve(t)
	h.login(t, s)
	deadline := time.Now().Add(10 * time.Second)
	for len(p.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	got := p.calls()
	if len(got) != 1 || !strings.HasPrefix(got[0], "CONNECT api.github.com:443") {
		t.Fatalf("with update_check on, serve reached %v, want api.github.com:443 once", got)
	}
	// The failed check does not stop kman: the summary still answers.
	if out, _ := h.mustRun(t); !strings.Contains(out, "kman is running") {
		t.Errorf("the summary after a failed check:\n%s", out)
	}
}
