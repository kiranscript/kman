package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing v0.1.0 prints or documents may send an operator to a command it does
// not ship (the security review, docs/security-review-v0.1.0.md): an operator
// who is told to run `kman backup` while a leak is being contained loses time
// they do not have. SPEC "Deferred past v0.1.0" lists them. When one ships, take
// it off this list.
var unshipped = []string{
	"backup", "restore", "prune", "scan", "exposure", "dashboard", "demo", "canary",
	"channel", "incident", "incidents", "detection", "detections", "known-source", "metrics",
}

var namesUnshipped = regexp.MustCompile("`?\\bkman (" + strings.Join(unshipped, "|") + ")\\b")

func TestNothingNamesACommandThisReleaseDoesNotShip(t *testing.T) {
	root := filepath.Join("..", "..")
	var sources []string
	for _, dir := range []string{"cmd", "internal", "docs", "examples", "packaging"} {
		filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch {
			case strings.HasSuffix(path, "_test.go"), strings.Contains(path, "testdata"):
			case strings.Contains(path, filepath.Join("docs", "adr")), strings.Contains(path, "security-review"):
			case strings.HasSuffix(path, ".go"), strings.HasSuffix(path, ".md"), strings.HasSuffix(path, ".yaml"),
				strings.HasSuffix(path, ".service"), strings.HasSuffix(path, ".json"):
				sources = append(sources, path)
			}
			return nil
		})
	}
	if len(sources) < 50 {
		t.Fatalf("found only %d sources: the walk is broken", len(sources))
	}
	for _, path := range sources {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasSuffix(path, ".go") {
				line = withoutComment(line) // a comment is for the next developer, not the operator
			}
			if m := namesUnshipped.FindString(line); m != "" {
				t.Errorf("%s:%d names %q, which this release does not ship", path, i+1, strings.TrimPrefix(m, "`"))
			}
		}
	}
}

// withoutComment cuts a Go line at a // that is outside a string.
func withoutComment(line string) string {
	for i := 0; i+1 < len(line); i++ {
		if line[i] == '/' && line[i+1] == '/' && strings.Count(line[:i], `"`)%2 == 0 && strings.Count(line[:i], "`")%2 == 0 {
			return line[:i]
		}
	}
	return line
}
