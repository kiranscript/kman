package releasecheck_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The v0.1.0 ticket asks that every item of M1's done criteria (SPEC,
// Milestones) is checked off against a test. docs/v0.1.0-done.md does it: one
// section per criterion, the criterion quoted exactly, and the evidence as
// `path::TestName` (a test that must exist) or `path` (a file that must).
// This holds the page to the spec and to the tree, so a renamed test or a new
// criterion fails CI instead of leaving the page quietly wrong.
func TestEveryM1DoneCriterionIsCheckedAgainstATestThatExists(t *testing.T) {
	spec := string(read(t, "SPEC.md"))
	_, after, ok := strings.Cut(spec, "### M1 Contain (`v0.1.0`)")
	if !ok {
		t.Fatal("SPEC.md has no M1 section")
	}
	_, done, ok := strings.Cut(after, "**Done when:**")
	if !ok {
		t.Fatal("the M1 section has no Done when list")
	}
	done, _, _ = strings.Cut(done, "\n### ")
	var criteria []string
	for _, line := range strings.Split(done, "\n") {
		if c, ok := strings.CutPrefix(line, "- "); ok {
			criteria = append(criteria, c)
		}
	}
	if len(criteria) < 7 {
		t.Fatalf("found %d done criteria in SPEC.md, want at least 7: %q", len(criteria), criteria)
	}

	page := string(read(t, "docs/v0.1.0-done.md"))
	sections := strings.Split(page, "\n## ")[1:]
	evidence := regexp.MustCompile("(?m)^- `([^`\\s]+)`")
	for _, c := range criteria {
		var section string
		for _, s := range sections {
			if strings.Contains(s, "> "+c+"\n") {
				section = s
			}
		}
		if section == "" {
			t.Errorf("docs/v0.1.0-done.md has no section quoting the criterion %q", c)
			continue
		}
		refs := evidence.FindAllStringSubmatch(section, -1)
		checked := 0
		for _, m := range refs {
			path, name, isTest := strings.Cut(m[1], "::")
			if err := evidenceExists(path, name, isTest); err != nil {
				t.Errorf("criterion %q: %v", c, err)
			}
			checked++
		}
		if checked == 0 {
			t.Errorf("criterion %q names no test or file as evidence", c)
		}
	}
	if got := len(sections); got != len(criteria) {
		t.Errorf("docs/v0.1.0-done.md has %d sections for %d criteria", got, len(criteria))
	}
}

func evidenceExists(path, name string, isTest bool) error {
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return fmt.Errorf("%s does not exist", path)
	}
	if isTest && !regexp.MustCompile(`(?m)^func `+regexp.QuoteMeta(name)+`\(`).Match(b) {
		return fmt.Errorf("%s has no func %s", path, name)
	}
	return nil
}
