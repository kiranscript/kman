package releasecheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkFixture runs scripts/check-fixture.sh for tag against a fixtures
// directory holding the named files, each a copy of a real SQLite header
// unless its content is given.
func checkFixture(t *testing.T, tag string, files map[string]string) error {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script, err := filepath.Abs(filepath.Join(root, "scripts", "check-fixture.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", script, tag, dir).CombinedOutput()
	t.Logf("check-fixture %s: %s", tag, out)
	return err
}

const sqliteHeader = "SQLite format 3\x00 and the rest of the file"

// SPEC §15, release checklist step 3: a tag with no committed fixture must
// not release.
func TestATagWithoutAFixtureFailsTheCheck(t *testing.T) {
	files := map[string]string{"v0.1.0.db": sqliteHeader}
	if err := checkFixture(t, "v0.1.0", files); err != nil {
		t.Errorf("a tag with its fixture: %v", err)
	}
	for _, tag := range []string{"v0.2.0", "v0.1.0-rc.1", "v0.1"} {
		if err := checkFixture(t, tag, files); err == nil {
			t.Errorf("tag %s has no fixture, but the check passed", tag)
		}
	}
	for name, content := range map[string]string{"empty": "", "not a database": "just text, long enough to look real"} {
		if err := checkFixture(t, "v0.1.0", map[string]string{"v0.1.0.db": content}); err == nil {
			t.Errorf("a fixture that is %s passed", name)
		}
	}
}

// The check runs in the release job before anything is built or published, so
// a missing fixture fails the release with nothing to clean up.
func TestTheReleaseJobChecksTheFixtureBeforeBuilding(t *testing.T) {
	j := loadWorkflow(t, "release.yml").Jobs["release"]
	check, build := -1, -1
	for i, st := range j.Steps {
		if check < 0 && strings.Contains(st.Run, "scripts/check-fixture.sh") {
			check = i
			if !strings.Contains(st.Run, `"$TAG"`) {
				t.Errorf("step %q does not check the pushed tag", st.Name)
			}
		}
		if build < 0 && strings.HasPrefix(st.Uses, "goreleaser/goreleaser-action@") {
			build = i
		}
	}
	if check < 0 {
		t.Fatal("the release job never runs scripts/check-fixture.sh")
	}
	if build < 0 || check > build {
		t.Errorf("the fixture check is step %d, GoReleaser step %d: check first", check, build)
	}
}
