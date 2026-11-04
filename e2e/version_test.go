package e2e_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// git runs git in the repository root and returns its trimmed output.
func git(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestVersionNamesTheCommitAndItsDate(t *testing.T) {
	commit := git(t, "rev-parse", "HEAD")
	unix := git(t, "log", "-1", "--format=%ct")
	var secs int64
	if _, err := fmt.Sscan(unix, &secs); err != nil {
		t.Fatalf("parse commit time %q: %v", unix, err)
	}
	built := time.Unix(secs, 0).UTC().Format(time.RFC3339)

	lines := strings.Split(strings.TrimSpace(runKman(t, "version")), "\n")
	if len(lines) != 3 {
		t.Fatalf("kman version printed %d lines, want 3:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], "kman ") {
		t.Errorf("line 1 = %q, want the version after %q", lines[0], "kman ")
	}
	if !strings.HasPrefix(lines[1], "commit: "+commit) {
		t.Errorf("line 2 = %q, want it to start with %q", lines[1], "commit: "+commit)
	}
	if want := "built:  " + built; lines[2] != want {
		t.Errorf("line 3 = %q, want %q (the commit's date)", lines[2], want)
	}
}
