package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/adminapi"
	"github.com/kirank55/kman/internal/cli"
	"github.com/kirank55/kman/internal/store"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// install is a running admin API over a fresh install, and a config
// directory for the CLI.
type install struct {
	url       string
	owner     string // the owner token's secret
	configDir string
	svc       *admin.Service
}

func newInstall(t *testing.T) *install {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := admin.New(admin.Options{Store: st, Now: func() time.Time { return t0 }})
	owner, err := svc.CreateAdminToken(admin.WithCaller(context.Background(), admin.Caller{Actor: "system:init"}),
		admin.CreateAdminTokenRequest{Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(adminapi.Handler(svc, slog.New(slog.DiscardHandler)))
	t.Cleanup(ts.Close)
	return &install{url: ts.URL, owner: owner.Secret, configDir: t.TempDir(), svc: svc}
}

// loggedIn writes the credentials file as init or login would.
func (in *install) loggedIn(t *testing.T) *install {
	t.Helper()
	if err := cli.SaveCredentials(in.credentials(), in.url, in.owner); err != nil {
		t.Fatal(err)
	}
	return in
}

func (in *install) credentials() string { return filepath.Join(in.configDir, cli.CredentialsFile) }

// result is one run of the CLI.
type result struct {
	stdout, stderr string
	code           int
}

// run runs the CLI with env as its environment and stdin as its input.
func (in *install) run(t *testing.T, env map[string]string, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), args, cli.Env{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(stdin),
		Getenv:    func(k string) string { return env[k] },
		ConfigDir: in.configDir,
	})
	return result{stdout.String(), stderr.String(), code}
}

// kman runs the CLI logged in through the credentials file and fails the
// test unless it exits 0.
func (in *install) kman(t *testing.T, args ...string) string {
	t.Helper()
	r := in.run(t, nil, "", args...)
	if r.code != 0 {
		t.Fatalf("kman %s exited %d:\n%s%s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

// kmanJSON runs the CLI with --json and decodes its output into v.
func (in *install) kmanJSON(t *testing.T, v any, args ...string) {
	t.Helper()
	out := in.kman(t, append(args, "--json")...)
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("kman %s --json printed what is not JSON (%v):\n%s", strings.Join(args, " "), err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
