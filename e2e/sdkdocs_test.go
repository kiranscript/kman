package e2e_test

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
)

var updateDocs = flag.Bool("update", false, "rewrite the generated parts of docs/sdks.md")

// docs/sdks.md shows the snippets in testdata/sdk, which this file runs through
// the real binary, so each snippet on that page is one that works. Its table
// of presets comes from the presets themselves. A region of the page between
//
//	<!-- snippet:NAME -->  and  <!-- /snippet -->
//	<!-- presets -->      and  <!-- /presets -->
//
// is generated; the rest is prose. Regenerate it with
//
//	go test ./e2e -run TheSDKDocIsCurrent -update
var snippets = []struct {
	name, path, lang string
}{
	{"curl", "curl.sh", "sh"},
	{"openai-python", "python/snippets/openai_chat.py", "python"},
	{"openai-node", "node/snippets/openai.mjs", "js"},
	{"anthropic-python", "python/snippets/anthropic_message.py", "python"},
	{"anthropic-node", "node/snippets/anthropic.mjs", "js"},
}

func TestTheSDKDocIsCurrent(t *testing.T) {
	path := filepath.Join("..", "docs", "sdks.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, s := range snippets {
		code, err := os.ReadFile(filepath.Join("testdata", "sdk", s.path))
		if err != nil {
			t.Fatal(err)
		}
		code = []byte(strings.ReplaceAll(string(code), "\r\n", "\n")) // a Windows checkout may have CRLF
		got = regenerate(t, got, "<!-- snippet:"+s.name+" -->", "<!-- /snippet -->",
			"```"+s.lang+"\n"+strings.TrimRight(string(code), "\n")+"\n```")
	}
	got = regenerate(t, got, "<!-- presets -->", "<!-- /presets -->", presetTable())
	if *updateDocs {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got != strings.ReplaceAll(string(raw), "\r\n", "\n") {
		t.Errorf("%s is stale: run go test ./e2e -run TheSDKDocIsCurrent -update", path)
	}
}

// regenerate replaces what is between open and close with body.
func regenerate(t *testing.T, doc, open, close, body string) string {
	t.Helper()
	i := strings.Index(doc, open)
	if i < 0 {
		t.Fatalf("docs/sdks.md has no %s", open)
	}
	start := i + len(open)
	j := strings.Index(doc[start:], close)
	if j < 0 {
		t.Fatalf("docs/sdks.md has no %s after %s", close, open)
	}
	return doc[:start] + "\n" + body + "\n" + doc[start+j:]
}

// presetTable lists every preset with the variable its SDK reads the key from
// and the base URL to give it, with kman at its default address.
func presetTable() string {
	var b strings.Builder
	b.WriteString("| Preset | Upstream | Key variable | Base URL |\n|---|---|---|---|\n")
	for _, p := range profiles.Presets() {
		env, base := "", "`http://127.0.0.1:8080"+p.Spec.SDKBasePath+"`"
		if p.Spec.SDKEnv != "" {
			env = "`" + p.Spec.SDKEnv + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s (`%s`) | %s | %s |\n", p.Name, p.Title, p.Spec.Host, env, base)
	}
	return strings.TrimRight(b.String(), "\n")
}

// The snippets on the page work: each, run as written under the pinned SDK it
// uses, with only the environment kman's quickstart tells an app to set, gets the
// transcript's answer through kman and is metered.
func TestTheSDKDocSnippetsRunThroughKman(t *testing.T) {
	cases := []struct {
		name, transcript, profile string
		base, keyVar, baseVar     string // the base path, and the variables the snippet reads
		command                   func(t *testing.T) []string
	}{
		{"curl", "openai-chat", "openai", "/v1", "OPENAI_API_KEY", "OPENAI_BASE_URL", func(t *testing.T) []string {
			sh, err := exec.LookPath("sh")
			if err != nil || runtime.GOOS == "windows" {
				t.Skip("the curl snippet is for a Unix shell")
			}
			if _, err := exec.LookPath("curl"); err != nil {
				t.Skip("curl is not installed")
			}
			return []string{sh, filepath.Join("testdata", "sdk", "curl.sh")}
		}},
		{"openai-python", "openai-chat", "openai", "/v1", "OPENAI_API_KEY", "OPENAI_BASE_URL", snippetCommand("python", "openai_chat.py")},
		{"openai-node", "openai-chat", "openai", "/v1", "OPENAI_API_KEY", "OPENAI_BASE_URL", snippetCommand("node", "openai.mjs")},
		{"anthropic-python", "anthropic-messages", "anthropic", "", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", snippetCommand("python", "anthropic_message.py")},
		{"anthropic-node", "anthropic-messages", "anthropic", "", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", snippetCommand("node", "anthropic.mjs")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			command := c.command(t)
			p := behindKmanAs(t, c.transcript, c.profile)
			p.setPrices("gpt-4o-mini", "gpt-4o-mini-2024-07-18", "claude-sonnet-5-5")
			cmd := exec.Command(command[0], command[1:]...)
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
				c.keyVar + "=" + p.secret, c.baseVar + "=http://" + p.s.proxy + c.base,
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(string(out), "Hello there") {
				t.Errorf("the snippet printed %q, want the transcript's answer", out)
			}
			p.mock.only(t)
			if spent := p.settled(1).Spend; spent.Today.CostMicro == 0 {
				t.Errorf("the snippet's request was not metered: %+v", spent)
			}
		})
	}
}

// snippetCommand runs a snippet with the pinned SDK of its language, skipping
// where it is not installed (and failing in CI's sdk-e2e job, as the other SDK
// tests do).
func snippetCommand(language, file string) func(t *testing.T) []string {
	return func(t *testing.T) []string {
		for _, rt := range sdkRuntimes {
			if rt.name != language {
				continue
			}
			command := rt.command(t) // skips if the SDK is missing
			dir := filepath.Dir(command[1])
			return []string{command[0], filepath.Join(dir, "snippets", file)}
		}
		t.Fatalf("no runtime %s", language)
		return nil
	}
}
