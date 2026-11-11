package cli_test

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/adminapi"
)

func TestLoginSavesTheURLAndTokenAndLogoutRemovesThem(t *testing.T) {
	in := newInstall(t)
	r := in.run(t, nil, in.owner+"\n", "login", "--admin-url", in.url)
	if r.code != 0 {
		t.Fatalf("login exited %d: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "owner") {
		t.Errorf("login did not say who it logged in as:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout+r.stderr, in.owner) {
		t.Error("login printed the token")
	}
	var saved struct {
		AdminURL string `json:"admin_url"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal([]byte(readFile(t, in.credentials())), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AdminURL != in.url || saved.Token != in.owner {
		t.Errorf("credentials file = %+v, want %s and the owner token", saved, in.url)
	}
	if fi, err := os.Stat(in.credentials()); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials file mode %v, want 0600", fi.Mode().Perm())
	}
	in.kman(t, "admin-token", "list")

	in.kman(t, "logout")
	if _, err := os.Stat(in.credentials()); !os.IsNotExist(err) {
		t.Errorf("logout left the credentials file: %v", err)
	}
	r = in.run(t, nil, "", "admin-token", "list")
	if r.code != 1 || !strings.Contains(r.stderr, "kman login") {
		t.Errorf("after logout: %d %q, want 1 and a hint to log in", r.code, r.stderr)
	}
	// Logging out twice is fine.
	in.kman(t, "logout")
}

func TestLoginReplacesAnEarlierLogin(t *testing.T) {
	in := newInstall(t)
	other := newInstall(t)
	if r := in.run(t, nil, other.owner+"\n", "login", "--admin-url", other.url); r.code != 0 {
		t.Fatal(r.stderr)
	}
	if r := in.run(t, nil, in.owner+"\n", "login", "--admin-url", in.url); r.code != 0 {
		t.Fatal(r.stderr)
	}
	if got := readFile(t, in.credentials()); !strings.Contains(got, in.url) || !strings.Contains(got, in.owner) {
		t.Errorf("credentials file after the second login:\n%s", got)
	}
}

func TestLoginRefusesATokenTheServerRefuses(t *testing.T) {
	in := newInstall(t)
	for name, stdin := range map[string]string{
		"a wrong token": "kma_0000000000000000000000000000000000000000000000000\n",
		"no token":      "\n",
	} {
		r := in.run(t, nil, stdin, "login", "--admin-url", in.url)
		if r.code != 1 {
			t.Errorf("login with %s exited %d, want 1", name, r.code)
		}
		if _, err := os.Stat(in.credentials()); !os.IsNotExist(err) {
			t.Errorf("login with %s saved a credentials file", name)
		}
	}
	for _, url := range []string{"ftp://example.com", "127.0.0.1:8081", "unix:relative.sock", ""} {
		if r := in.run(t, nil, in.owner+"\n", "login", "--admin-url", url); r.code != 1 {
			t.Errorf("login --admin-url %q exited %d, want 1", url, r.code)
		}
	}
}

func TestKMANAdminTokenOverridesTheCredentialsFile(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "admin-token", "create", "ci")
	ci := secretRE.FindString(out)
	r := in.run(t, map[string]string{"KMAN_ADMIN_TOKEN": ci}, "", "settings", "set", "spend_timezone", "Europe/Berlin")
	if r.code != 0 {
		t.Fatal(r.stderr)
	}
	audit := in.kman(t, "audit")
	last := strings.Fields(strings.Split(strings.TrimSpace(audit), "\n")[len(strings.Split(strings.TrimSpace(audit), "\n"))-1])
	if !contains(last, "ci") || !contains(last, "settings.set") {
		t.Errorf("the last audit line is %v, want ci's settings.set", last)
	}
	// A wrong KMAN_ADMIN_TOKEN is not rescued by the file.
	r = in.run(t, map[string]string{"KMAN_ADMIN_TOKEN": "kma_wrong"}, "", "admin-token", "list")
	if r.code != 1 || !strings.Contains(r.stderr, "KMAN_ADMIN_TOKEN") {
		t.Errorf("a wrong KMAN_ADMIN_TOKEN: %d %q, want 1 naming it", r.code, r.stderr)
	}
}

func TestKMANAdminURLOverridesTheCredentialsFile(t *testing.T) {
	in := newInstall(t)
	if err := os.WriteFile(in.credentials(), []byte(`{"admin_url":"http://`+closedPort(t)+`","token":"`+in.owner+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := in.run(t, map[string]string{"KMAN_ADMIN_URL": in.url}, "", "admin-token", "list"); r.code != 0 {
		t.Errorf("with KMAN_ADMIN_URL: %d %s", r.code, r.stderr)
	}
	r := in.run(t, nil, "", "admin-token", "list")
	if r.code != 1 || !strings.Contains(r.stderr, "cannot reach kman") || !strings.Contains(r.stderr, "kman serve") {
		t.Errorf("an unreachable server: %d %q, want 1 naming the URL and kman serve", r.code, r.stderr)
	}
}

// closedPort is a loopback address nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func contains(fields []string, s string) bool {
	for _, f := range fields {
		if f == s {
			return true
		}
	}
	return false
}

func TestTheAdminURLMayBeAUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kman serves its admin socket on Unix only")
	}
	in := newInstall(t)
	// Socket paths are short-lived and short: macOS allows 104 bytes.
	dir, err := os.MkdirTemp("", "kman")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "admin.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: adminapi.Handler(in.svc, slog.New(slog.DiscardHandler))}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	if r := in.run(t, nil, in.owner+"\n", "login", "--admin-url", "unix:"+sock); r.code != 0 {
		t.Fatalf("login over the socket: %d %s", r.code, r.stderr)
	}
	in.kman(t, "admin-token", "list")
}
