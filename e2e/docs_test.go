package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// The README shows the quickstart's lines, and they must stay the same lines.
func TestTheREADMEQuickstartIsTheQuickstart(t *testing.T) {
	want := docSteps(t, "docs/quickstart.md", "quickstart")
	if got := docSteps(t, "README.md", "quickstart"); !slices.Equal(got, want) {
		t.Errorf("README.md's quickstart lines differ from docs/quickstart.md's:\n%q\n%q", got, want)
	}
}

// The commands docs/policy.md shows are accepted, one after the other, by a
// running kman. Each replaces the key's whole policy, as it says.
func TestThePolicyDocExamplesAreAccepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the examples are for a Unix shell")
	}
	p := behindKmanAs(t, "openai-chat", "openai")
	h := p.h.with("PATH=" + kmanOnPath(t) + string(os.PathListSeparator) + os.Getenv("PATH"))
	lines := docSteps(t, "docs/policy.md", "policy")
	if len(lines) < 5 {
		t.Fatalf("docs/policy.md has only %d example lines: %q", len(lines), lines)
	}
	runScript(t, h, "", lines)
	// The last example leaves the key with the policy the doc says it does.
	out, _ := h.mustRun(t, "key", "show", "my-app", "--json")
	var got spendOf
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("key show --json printed %q: %v", out, err)
	}
	var policy map[string]int
	if err := json.Unmarshal(got.Policy, &policy); err != nil || policy["daily_cap_micro"] != 5000000 || policy["monthly_cap_micro"] != 50000000 || policy["rpm_limit"] != 120 {
		t.Errorf("after the policy examples the key's policy is %s, not the last one shown", got.Policy)
	}
}

// kmanOnPath is a directory holding the binary under test as plain `kman`, for
// a doc's lines to run.
func kmanOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(kman, filepath.Join(dir, "kman")); err != nil {
		t.Fatal(err)
	}
	return dir
}
