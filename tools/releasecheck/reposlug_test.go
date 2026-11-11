package releasecheck_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The repository's owner and name are written in many files: the Go module
// path, every URL, the signing identity operators verify against. They change
// together, in one command, so renaming the repository before the first tag is
// not a hunt. scripts/set-repo.sh is that command. These tests hold it to its
// word and hold docs/releasing.md's list of places to the tree.

// trackedText returns the repository's tracked files that are text, relative
// to the root, with their contents.
func trackedText(t *testing.T) map[string][]byte {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // deleted in the working tree
		}
		if bytes.IndexByte(b, 0) >= 0 {
			continue // binary: fixtures
		}
		files[f] = b
	}
	return files
}

// copyTree writes files into a new git repository, so the script, which works
// on the files git tracks, can run on a copy.
func copyTree(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for f, b := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(filepath.Join(root, f)); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(p, b, mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func setRepo(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join(root, "scripts", "set-repo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The current slug is the Go module path's owner and name.
const slug = "kirank55/kman"

func TestSetRepoMovesEveryPlaceTheRepoIsNamed(t *testing.T) {
	files := trackedText(t)
	dir := copyTree(t, files)
	if out, err := setRepo(t, dir, "kiranscript/kman"); err != nil {
		t.Fatalf("set-repo: %v\n%s", err, out)
	}
	var left []string
	for f := range files {
		if f == "tools/releasecheck/reposlug_test.go" {
			continue // names the old owner to say what it checks
		}
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			// kirank55/keman is the old project's repository, not this one.
			if strings.Contains(line, "kirank55") && !strings.Contains(line, "kirank55/keman") && !strings.Contains(line, "noreply.github.com") {
				left = append(left, f+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	if len(left) > 0 {
		t.Errorf("the old owner is still named after set-repo:\n%s", strings.Join(left, "\n"))
	}
	for _, f := range []string{"go.mod", "tools/go.mod"} {
		if b, _ := os.ReadFile(filepath.Join(dir, f)); !strings.Contains(string(b), "module github.com/kiranscript/kman") {
			t.Errorf("%s was not moved: %q", f, b)
		}
	}
	// Idempotent: nothing differs the second time.
	before := snapshot(t, dir, files)
	if out, err := setRepo(t, dir, "kiranscript/kman"); err != nil {
		t.Fatalf("second set-repo: %v\n%s", err, out)
	}
	if after := snapshot(t, dir, files); after != before {
		t.Error("a second set-repo with the same slug changed files")
	}
	// The moved tree still builds, so no import path was left behind.
	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Errorf("go build ./... after set-repo: %v\n%s", err, out)
	}
}

func TestSetRepoRefusesASlugThatIsNotOwnerSlashName(t *testing.T) {
	dir := copyTree(t, map[string][]byte{"go.mod": []byte("module github.com/" + slug + "\n")})
	for _, bad := range []string{"", "kman", "a/b/c", "Owner/Kman", "owner/kman extra", "owner/../kman", "own er/kman"} {
		if out, err := setRepo(t, dir, bad); err == nil {
			t.Errorf("set-repo %q succeeded: %s", bad, out)
		}
	}
}

func snapshot(t *testing.T, dir string, files map[string][]byte) string {
	t.Helper()
	var names []string
	for f := range files {
		names = append(names, f)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, f := range names {
		c, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(f + "\x00" + string(c) + "\x00")
	}
	return b.String()
}

// docs/releasing.md lists every file that names the repository, so the
// maintainer who renames it can check the command's work. Go files are one
// line in the list: each imports the module path.
func TestTheReleasingGuideListsEveryFileThatNamesTheRepo(t *testing.T) {
	guide := string(read(t, "docs/releasing.md"))
	for f, b := range trackedText(t) {
		if strings.HasSuffix(f, ".go") || !strings.Contains(string(b), "kirank55") {
			continue
		}
		if !strings.Contains(guide, "`"+f+"`") {
			t.Errorf("docs/releasing.md does not list %s, which names the repository", f)
		}
	}
}
