package releasecheck_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A pinned action names a full commit SHA and, for Dependabot and readers,
// the version it stands for: "uses: owner/repo@<40 hex> # v1.2.3".
var pinnedUses = regexp.MustCompile(`^\s*-?\s*uses:\s*[\w.-]+/[\w./-]+@[0-9a-f]{40}\s+#\s*v\d+(\.\d+)*\s*$`)

func TestEveryActionIsPinnedByCommitSHA(t *testing.T) {
	for _, name := range workflowFiles(t) {
		text := string(read(t, filepath.Join(".github", "workflows", name)))
		for i, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			if !strings.HasPrefix(trimmed, "uses:") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(trimmed, "uses:")), "./") {
				continue // a local action or workflow is pinned by the checkout
			}
			if !pinnedUses.MatchString(line) {
				t.Errorf("%s:%d: %q is not pinned to a commit SHA with a version comment", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// Scorecard's Token-Permissions check: read-only at the top level, and every
// write granted to the one job that needs it.
func TestEveryWorkflowDefaultsToReadOnlyPermissions(t *testing.T) {
	for _, name := range workflowFiles(t) {
		w := loadWorkflow(t, name)
		perms, ok := w.Permissions.(map[string]any)
		if !ok {
			t.Errorf("%s: top-level permissions = %#v, want a map of read-only scopes", name, w.Permissions)
			continue
		}
		for scope, level := range perms {
			if level != "read" && level != "none" {
				t.Errorf("%s: top-level permission %s: %v, want read or none", name, scope, level)
			}
		}
	}
}

// harden-runner audits each job's network egress. It must be the first step,
// so it sees everything the job does, and in audit mode, so it never blocks.
func TestEveryJobStartsWithHardenRunnerInAuditMode(t *testing.T) {
	for _, name := range workflowFiles(t) {
		w := loadWorkflow(t, name)
		for id, j := range w.Jobs {
			where := fmt.Sprintf("%s: job %s", name, id)
			if len(j.Steps) == 0 {
				t.Errorf("%s has no steps", where)
				continue
			}
			first := j.Steps[0]
			if actionPath(first.Uses) != "step-security/harden-runner" {
				t.Errorf("%s: first step uses %q, want step-security/harden-runner", where, first.Uses)
				continue
			}
			if first.With["egress-policy"] != "audit" {
				t.Errorf("%s: harden-runner egress-policy = %q, want audit", where, first.With["egress-policy"])
			}
		}
	}
}

// Interpreter caches are build output: a committed .pyc is a binary nobody
// reviewed that Python trusts, and it changes with the interpreter. The
// SDK e2e snippets run under Python, which writes them next to the snippets.
func TestNoInterpreterCacheIsCommitted(t *testing.T) {
	if !strings.Contains(string(read(t, ".gitignore")), "__pycache__/") {
		t.Error(".gitignore has no __pycache__/ rule")
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	for _, f := range strings.Split(string(out), "\x00") {
		if strings.Contains(f, "__pycache__/") || strings.HasSuffix(f, ".pyc") {
			t.Errorf("%s is committed; remove it (git rm) and keep it ignored", f)
		}
	}
}
