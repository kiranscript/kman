package e2e_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The SDKs are pinned under testdata/sdk and installed by its install.sh. CI's
// sdk-e2e job sets this variable, so that a missing SDK fails there rather than
// skips.
const sdkRequired = "KMAN_E2E_SDK"

// A sdkRuntime runs a scenario under one language's SDKs.
type sdkRuntime struct {
	name    string
	command func(t *testing.T) []string
}

var sdkRuntimes = []sdkRuntime{
	{"python", func(t *testing.T) []string {
		dir, err := filepath.Abs(filepath.Join("testdata", "sdk", "python"))
		if err != nil {
			t.Fatal(err)
		}
		python := filepath.Join(dir, ".venv", "bin", "python")
		if runtime.GOOS == "windows" {
			python = filepath.Join(dir, ".venv", "Scripts", "python.exe")
		}
		needInstalled(t, python)
		return []string{python, filepath.Join(dir, "run.py")}
	}},
	{"node", func(t *testing.T) []string {
		dir, err := filepath.Abs(filepath.Join("testdata", "sdk", "node"))
		if err != nil {
			t.Fatal(err)
		}
		needInstalled(t, filepath.Join(dir, "node_modules", "openai"))
		node, err := exec.LookPath("node")
		if err != nil {
			needInstalled(t, "node")
		}
		return []string{node, filepath.Join(dir, "run.mjs")}
	}},
}

func needInstalled(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		return
	}
	if os.Getenv(sdkRequired) != "" {
		t.Fatalf("%s is missing: run e2e/testdata/sdk/install.sh", path)
	}
	t.Skipf("%s is missing: run e2e/testdata/sdk/install.sh", path)
}

// authBearer and authAPIKey are the headers each kind of upstream's credential
// goes in.
const (
	authBearer = "Authorization"
	authAPIKey = "X-Api-Key"
)

var sdkScenarios = []struct {
	name, profile, basePath, authHeader string
	wantText                            string
	wantUsage                           map[string]int64 // what the SDK must have made of the usage
	wantAsked                           bool             // the upstream must be asked for a stream's usage
	// wantSpend is what the key must have been charged, in microdollars. The test
	// prices every model at 1 µ$ an input token and 2 an output token, cached and
	// cache-write tokens at the input price, so each is worked out by hand from the
	// transcript's usage (see its "usage") rather than from kman's own sums.
	wantSpend int64
	// models are the ids the scenario asks for and the response names, which the
	// test prices.
	models []string
}{
	{name: "openai-chat", profile: "openai", basePath: "/v1", authHeader: authBearer, wantText: "Hello there",
		wantUsage: map[string]int64{"prompt_tokens": 12, "completion_tokens": 5}, wantSpend: 22, models: []string{"gpt-4o-mini", "gpt-4o-mini-2024-07-18"}},
	{name: "openai-chat-stream", profile: "openai", basePath: "/v1", authHeader: authBearer, wantText: "Hello there", wantAsked: true,
		wantUsage: map[string]int64{"prompt_tokens": 12, "completion_tokens": 5}, wantSpend: 22, models: []string{"gpt-4o-mini", "gpt-4o-mini-2024-07-18"}},
	{name: "openai-completions", profile: "openai", basePath: "/v1", authHeader: authBearer, wantText: "Hello there",
		wantUsage: map[string]int64{"prompt_tokens": 3, "completion_tokens": 2}, wantSpend: 7, models: []string{"gpt-3.5-turbo-instruct"}},
	{name: "openai-completions-stream", profile: "openai", basePath: "/v1", authHeader: authBearer, wantText: "Hello there", wantAsked: true,
		wantUsage: map[string]int64{"prompt_tokens": 3, "completion_tokens": 2}, wantSpend: 7, models: []string{"gpt-3.5-turbo-instruct"}},
	{name: "openai-embeddings", profile: "openai", basePath: "/v1", authHeader: authBearer, wantText: "0.5,-0.25",
		wantUsage: map[string]int64{"prompt_tokens": 6, "total_tokens": 6}, wantSpend: 6, models: []string{"text-embedding-3-small"}},
	{name: "openai-responses", profile: "openai", basePath: "/v1", authHeader: authBearer, wantText: "Hello there",
		wantUsage: map[string]int64{"input_tokens": 20, "output_tokens": 7}, wantSpend: 34, models: []string{"gpt-5", "gpt-5-2026-01-01"}},
	{name: "openai-responses-stream", profile: "openai", basePath: "/v1", authHeader: authBearer, wantText: "Hello there",
		wantUsage: map[string]int64{"input_tokens": 20, "output_tokens": 7}, wantSpend: 34, models: []string{"gpt-5", "gpt-5-2026-01-01"}},
	{name: "anthropic-messages", profile: "anthropic", authHeader: authAPIKey, wantText: "Hello there",
		wantUsage: map[string]int64{"input_tokens": 25, "output_tokens": 9, "cache_read_input_tokens": 10}, wantSpend: 59, models: []string{"claude-sonnet-5-5"}},
	{name: "anthropic-messages-stream", profile: "anthropic", authHeader: authAPIKey, wantText: "Hello there",
		wantUsage: map[string]int64{"input_tokens": 25, "output_tokens": 9, "cache_read_input_tokens": 10}, wantSpend: 59, models: []string{"claude-sonnet-5-5"}},
	{name: "anthropic-error", profile: "anthropic", authHeader: authAPIKey, wantText: "rate_limit_error", wantSpend: 0, models: []string{"claude-sonnet-5-5"}},
}

// The official SDKs, pinned, work through the real binary, against transcripts:
// with the virtual key as their API key, and kman's address as their base URL, and
// no other change. The transcripts' notes say where each came from.
func TestOfficialSDKsWorkThroughKman(t *testing.T) {
	for _, rt := range sdkRuntimes {
		t.Run(rt.name, func(t *testing.T) {
			command := rt.command(t)
			for _, sc := range sdkScenarios {
				t.Run(sc.name, func(t *testing.T) {
					t.Parallel()
					p := behindKmanAs(t, sc.name, sc.profile)
					p.setPrices(sc.models...)
					cmd := exec.Command(command[0], append(command[1:], sc.name)...)
					cmd.Env = []string{
						"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
						"KMAN_BASE_URL=http://" + p.s.proxy + sc.basePath, "KMAN_KEY=" + p.secret,
					}
					out, err := cmd.Output()
					if err != nil {
						var stderr []byte
						if ee, ok := errors.AsType[*exec.ExitError](err); ok {
							stderr = ee.Stderr
						}
						t.Fatalf("%v\n%s\n%s", err, out, stderr)
					}
					var got struct {
						Text  string         `json:"text"`
						Usage map[string]any `json:"usage"`
					}
					if err := json.Unmarshal(out, &got); err != nil {
						t.Fatalf("%v: %s", err, out)
					}
					if got.Text != sc.wantText {
						t.Errorf("the SDK made %q of the answer, want %q", got.Text, sc.wantText)
					}
					for field, want := range sc.wantUsage {
						if n, ok := got.Usage[field].(float64); !ok || int64(n) != want {
							t.Errorf("the SDK's usage has %s = %v, want %d: %v", field, got.Usage[field], want, got.Usage)
						}
					}
					req := p.mock.only(t)
					wantForwardedAsAMeteredRequest(t, req, p.secret, sc.authHeader, sc.wantAsked)
					// The spend is metered, and visible per key.
					spent := p.settled(1).Spend
					if spent.Today.CostMicro != sc.wantSpend || spent.Month.CostMicro != sc.wantSpend || spent.Today.EstimatedMicro != 0 {
						t.Errorf("the key was charged %+v this month, want %d µ$ reported and none estimated", spent, sc.wantSpend)
					}
				})
			}
		})
	}
}

func wantForwardedAsAMeteredRequest(t *testing.T, req received, secret, authHeader string, asked bool) {
	t.Helper()
	want := sentinel
	if authHeader == authBearer {
		want = "Bearer " + sentinel
	}
	if got := req.Header.Values(authHeader); len(got) != 1 || got[0] != want {
		t.Errorf("%s = %q", authHeader, got)
	}
	for name, values := range req.Header {
		for _, v := range values {
			if strings.Contains(v, secret) {
				t.Errorf("%s reached the upstream holding the virtual key", name)
			}
		}
	}
	if strings.Contains(req.Body, secret) {
		t.Error("the body reached the upstream holding the virtual key")
	}
	if got := req.Header.Get("Accept-Encoding"); got != "identity" {
		t.Errorf("Accept-Encoding = %q", got)
	}
	var body struct {
		Options *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("the upstream got %q: %v", req.Body, err)
	}
	if asked != (body.Options != nil && body.Options.IncludeUsage) {
		t.Errorf("asked for usage = %v, want %v: %s", body.Options, asked, req.Body)
	}
}
