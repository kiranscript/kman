package releasecheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const changelog = `# Changelog

## [Unreleased]

- Not yet.

## [0.2.0-rc.1] - 2026-12-01

- Detect.

## [0.1.0] - 2026-11-20

### Added

- Contain.

[0.1.0]: https://github.com/kirank55/kman/releases/tag/v0.1.0
`

// releaseNotes runs scripts/release-notes.sh for tag against a changelog.
func releaseNotes(t *testing.T, tag, text string) (string, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join(root, "scripts", "release-notes.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", script, tag, file).Output()
	return string(out), err
}

func TestReleaseNotesAreTheTagsChangelogSection(t *testing.T) {
	got, err := releaseNotes(t, "v0.1.0", changelog)
	if err != nil {
		t.Fatal(err)
	}
	want := "### Added\n\n- Contain.\n"
	if got != want {
		t.Errorf("notes = %q, want %q", got, want)
	}
	got, err = releaseNotes(t, "v0.2.0-rc.1", changelog)
	if err != nil || got != "- Detect.\n" {
		t.Errorf("rc notes = %q, %v, want the rc section", got, err)
	}
}

// A tag without a final section fails the release before anything is built.
func TestReleaseNotesFailWithoutASection(t *testing.T) {
	for _, tag := range []string{"v0.3.0", "v0.1", "Unreleased"} {
		if out, err := releaseNotes(t, tag, changelog); err == nil {
			t.Errorf("tag %s: notes %q, want an error", tag, out)
		}
	}
	empty := strings.Replace(changelog, "- Detect.\n", "", 1)
	if out, err := releaseNotes(t, "v0.2.0-rc.1", empty); err == nil {
		t.Errorf("empty section: notes %q, want an error", out)
	}
}
