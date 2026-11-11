package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The quickstart, docs/quickstart.md, is run as written: the lines between its
// quickstart markers are the ones a new operator types, and this test runs
// exactly those, in order, with nothing added to them and nothing taken away.
//
// What it cannot do is reach the internet, so the machine around the lines is
// made to look like the real one. A shim curl answers the two GitHub URLs the
// lines use (raw.githubusercontent.com/.../install.sh and the release
// downloads) from the repository and a release packed from the binary under
// test; every other URL fails. The proxy and admin listeners are moved off 8080
// and 8081 with the environment variables `kman init` reads, since a test
// machine may use those ports. `kman serve` stays in the foreground as it does
// for an operator, so the test starts it in the background at its line, as a
// second terminal would. The OpenAI key the line `upstream add` asks for is
// typed on its standard input.
//
// Then the install is used: a second upstream, an OpenAI-profile one on a mock,
// stands in for api.openai.com, and a request through the quickstart's server is
// metered.
func TestTheQuickstartRunsAsWritten(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the quickstart is for Linux and macOS; Windows has its own steps")
	}
	notInContainer(t)
	for _, tool := range []string{"curl", "sh", "tar", "gzip", "uname", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}

	lines := docSteps(t, "docs/quickstart.md", "quickstart")
	serveAt := indexOf(lines, "kman serve")
	if serveAt < 0 {
		t.Fatalf("the quickstart has no `kman serve` line: %q", lines)
	}

	h := newHome(t)
	bin := filepath.Join(h.root, "bin")
	proxyPort, adminPort := freePort(t), freePort(t)
	h = h.with(
		"PATH="+quickstartPath(t, h.root, bin),
		"KMAN_INSTALL_DIR="+bin,
		fmt.Sprintf("KMAN_PROXY_LISTEN=127.0.0.1:%d", proxyPort),
		fmt.Sprintf("KMAN_ADMIN_LISTEN=127.0.0.1:%d", adminPort),
	)

	// Everything before `kman serve`: install, then init.
	before := runScript(t, h, "", lines[:serveAt])
	if !strings.Contains(before.stderr, "installed "+filepath.Join(bin, "kman")) {
		t.Errorf("the install line did not say it installed into %s:\n%s", bin, before.stderr)
	}
	if !strings.Contains(before.stderr, "signature was NOT verified") {
		t.Errorf("with no cosign or gh the install line must warn that the signature was not checked:\n%s", before.stderr)
	}
	if got, err := os.ReadFile(filepath.Join(bin, "kman")); err != nil {
		t.Fatalf("the install line left no binary: %v", err)
	} else if want, _ := os.ReadFile(kman); string(got) != string(want) {
		t.Error("the installed kman is not the release's binary")
	}
	if !regexp.MustCompile(`kma_[0-9A-Za-z]{40,}`).MatchString(before.stdout) {
		t.Errorf("kman init did not print the owner admin token:\n%s", before.stdout)
	}

	s := h.serve(t)
	if want := fmt.Sprintf("127.0.0.1:%d", proxyPort); s.proxy != want {
		t.Fatalf("the proxy listens on %s, want %s", s.proxy, want)
	}

	// Everything after it. The last line is not the quickstart's: it reports
	// what the export line set.
	after := runScript(t, h, "sk-quickstart-not-a-real-credential\n",
		append(append([]string(nil), lines[serveAt+1:]...), `printf 'BASE_URL=%s\n' "$OPENAI_BASE_URL"`))
	if want := "BASE_URL=http://127.0.0.1:8080/v1"; !strings.Contains(after.stdout, want) {
		t.Errorf("the quickstart's export line did not set the base URL to kman's default proxy address (%s):\n%s", want, after.stdout)
	}
	if strings.Contains(after.stdout+after.stderr, "sk-quickstart-not-a-real-credential") {
		t.Error("the upstream's credential was echoed back")
	}
	m := regexp.MustCompile(`(?m)^\s*export OPENAI_API_KEY=(kmn_\S+)$`).FindStringSubmatch(after.stdout)
	if m == nil {
		t.Fatalf("key create did not print an export line for the key:\n%s", after.stdout)
	}
	secret := m[1]
	if !strings.Contains(after.stdout, "Created key my-app") {
		t.Errorf("key create did not say it made my-app:\n%s", after.stdout)
	}

	// The plain command says where the install stands: ready.
	if out, _ := h.mustRun(t); !strings.Contains(out, "kman is ready") {
		t.Errorf("after the quickstart plain kman says:\n%s", out)
	}

	// The quickstart's key is a real, working virtual key: kman authenticates it
	// and then refuses a route the preset does not allow, without a word to OpenAI.
	resp, body := sendThrough(t, s, "POST", "/v1/batches", strings.NewReader(`{}`), httpBearer(secret))
	wantError(t, resp, body, 403, "path_not_allowed")

	// The next lines of the quickstart: cap the key, look at it, kill it.
	next := runScript(t, h, "", docSteps(t, "docs/quickstart.md", "quickstart-next"))
	for _, want := range []string{"spend today:  $0 of $5 cap", "Revoked key my-app"} {
		if !strings.Contains(next.stdout, want) {
			t.Errorf("the quickstart's last lines did not print %q:\n%s", want, next.stdout)
		}
	}
	resp, body = sendThrough(t, s, "POST", "/v1/batches", strings.NewReader(`{}`), httpBearer(secret))
	wantError(t, resp, body, 401, "key_revoked")

	// And a key on an OpenAI-profile upstream is metered. The mock stands in for
	// api.openai.com, which the test cannot reach.
	mock := newMock(t, "openai-chat")
	h.addUpstream(t, "mock", mock.host, mock.port, "--profile", "openai")
	h.mustRun(t, "price", "set", "gpt-4o-mini", "--upstream", "mock", "--input", "1", "--output", "2", "--max-output", "1000")
	h.mustRun(t, "price", "set", "gpt-4o-mini-2024-07-18", "--upstream", "mock", "--input", "1", "--output", "2", "--max-output", "1000")
	mockKey := h.createKey(t, "mock-app", "mock")
	resp, body = sendThrough(t, s, "POST", "/v1/chat/completions", strings.NewReader(chatRequest), httpBearer(mockKey))
	if resp.StatusCode != 200 || !strings.Contains(body, "Hello there") {
		t.Fatalf("the chat request through the quickstart's kman = %d:\n%s", resp.StatusCode, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, _ := h.mustRun(t, "key", "show", "mock-app", "--json")
		var got spendOf
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("key show --json printed %q: %v", out, err)
		}
		// 12 input tokens at 1 µ$ and 5 output at 2 µ$, from the transcript.
		if got.Spend.Today.CostMicro == 22 && got.Spend.Today.ReservedMicro == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the request was never metered at 22 µ$: %+v", got.Spend.Today)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// docSteps is the sh lines of the doc at doc (from the repository root) between its <!-- name:begin --> and
// <!-- name:end --> markers, as scripts/doc-steps.sh prints them (CI's
// release-snapshot job reads the Docker and systemd guides the same way).
func docSteps(t *testing.T, doc, name string) []string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	out, err := exec.Command(sh, filepath.Join("..", "scripts", "doc-steps.sh"), filepath.Join("..", doc), name).Output()
	if err != nil {
		t.Fatalf("doc-steps %s %s: %v", doc, name, err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func indexOf(lines []string, want string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == want {
			return i
		}
	}
	return -1
}

type scriptOutput struct{ stdout, stderr string }

// runScript runs lines as one sh script that stops at its first failure, with
// input on its standard input.
func runScript(t *testing.T, h *home, input string, lines []string) scriptOutput {
	t.Helper()
	cmd := exec.Command("sh", "-e", "-c", strings.Join(lines, "\n"))
	cmd.Dir, cmd.Env = h.root, h.env
	cmd.Stdin = strings.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	timer := time.AfterFunc(60*time.Second, func() { cmd.Process.Kill() })
	defer timer.Stop()
	if err := cmd.Run(); err != nil {
		t.Fatalf("the quickstart lines %q failed: %v\nstdout:\n%s\nstderr:\n%s", lines, err, out.String(), errb.String())
	}
	return scriptOutput{out.String(), errb.String()}
}

func httpBearer(secret string) map[string][]string {
	return map[string][]string{"Authorization": {"Bearer " + secret}}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// quickstartPath builds the PATH the quickstart's lines run under: a shim curl
// first, the system tools install.sh and the lines need (and no cosign or gh,
// which would try to verify the fake release), and the install directory.
func quickstartPath(t *testing.T, root, bin string) string {
	t.Helper()
	shims, tools, releaseDir := filepath.Join(root, "shims"), filepath.Join(root, "tools"), filepath.Join(root, "release")
	for _, d := range []string{shims, tools, releaseDir, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installSh, err := filepath.Abs(filepath.Join("..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRelease(t, "v0.1.0")
	for name, data := range r.files {
		if err := os.WriteFile(filepath.Join(releaseDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shim := fmt.Sprintf(`#!/bin/sh
# The quickstart test's curl: answers the GitHub URLs the quickstart uses from
# disk, and refuses every other.
out=; url=
while [ $# -gt 0 ]; do
	case $1 in
	-o) out=$2; shift ;;
	--proto | --proto-redir | -w) shift ;;
	-*) ;;
	*) url=$1 ;;
	esac
	shift
done
case $url in
https://raw.githubusercontent.com/kirank55/kman/main/install.sh) cat %q ;;
https://github.com/kirank55/kman/releases/latest) printf '%%s' https://github.com/kirank55/kman/releases/tag/v0.1.0 ;;
https://github.com/kirank55/kman/releases/download/v0.1.0/*) cp %q/"${url##*/}" "$out" ;;
*) echo "curl: the quickstart test has no network for $url" >&2; exit 22 ;;
esac
`, installSh, releaseDir)
	if err := os.WriteFile(filepath.Join(shims, "curl"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{
		"sh", "tar", "gzip", "sha256sum", "shasum", "uname", "mktemp", "rm", "mkdir", "chmod", "mv", "cp",
		"awk", "grep", "sed", "cat", "head", "tail", "tr", "id", "dirname", "basename", "wc", "cut", "printf", "env",
	} {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(tools, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return strings.Join([]string{shims, tools, bin}, string(os.PathListSeparator))
}
