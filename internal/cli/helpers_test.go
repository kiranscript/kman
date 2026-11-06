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

// priceTableAt is when the test price table was generated: 3 days before t0.
var priceTableAt = t0.AddDate(0, 0, -3)

func withPricesGenerated(at time.Time) func(*admin.PriceTable) {
	return func(p *admin.PriceTable) { p.GeneratedAt = at }
}

// testPrices is a small price table with one row of each shape.
func testPrices() admin.PriceTable {
	cached, webSearch := int64(250000), admin.PriceToolFee{Tool: "web_search", Micro: 10000}
	return admin.PriceTable{GeneratedAt: priceTableAt, StaleAfter: 60 * 24 * time.Hour, Models: []admin.PriceModel{
		{Model: "gpt-5.4", Mode: "chat", InputMicro: 2500000, OutputMicro: 15000000, CachedMicro: &cached, MaxOutput: 128000,
			LongContext:  []admin.PriceBand{{AboveTokens: 272000, InputMicro: 5000000, OutputMicro: 22500000}},
			ServiceTiers: []string{"flex", "priority"}, ToolFees: []admin.PriceToolFee{webSearch}},
		{Model: "gpt-live-1", Mode: "realtime", PerMinuteMicro: 50000, PerTextInputMicro: 4000},
		{Preset: "groq", Model: "openai/gpt-oss-120b", Mode: "chat", InputMicro: 150000, OutputMicro: 600000, MaxOutput: 65536, Reviewed: true},
	}}
}

func newInstall(t *testing.T, opts ...func(*admin.PriceTable)) *install {
	t.Helper()
	prices := testPrices()
	for _, o := range opts {
		o(&prices)
	}
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := admin.New(admin.Options{Store: st, Now: func() time.Time { return t0 },
		Prices: func() admin.PriceTable { return prices }})
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

// status runs the status summary, with local as this binary's own price data.
func (in *install) status(t *testing.T, local admin.PriceDataStatus) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cli.Status(context.Background(), cli.Env{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv:         func(string) string { return "" },
		ConfigDir:      in.configDir,
		LocalPriceData: func() admin.PriceDataStatus { return local },
	})
	return stdout.String()
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
