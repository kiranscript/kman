// Package gates holds the repo-wide quality gates that run inside go test:
// the module import rule from SPEC §2 and ADR 0006, no untested package
// under internal/, and the clock seam from SPEC §17.
//
// Each check takes the packages from Load and returns its violations, so a
// test can run it against this repository and against a planted fixture.
package gates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Package is one Go package as reported by go list.
type Package struct {
	ImportPath   string
	Name         string
	Dir          string
	Doc          string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
	Imports      []string
	Module       *struct{ Path string }
}

// Rel returns the package's path relative to its module, such as
// "internal/gateway".
func (p Package) Rel() string {
	if p.Module == nil {
		return p.ImportPath
	}
	return strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")
}

// TestFiles returns the package's test files, in-package and external.
func (p Package) TestFiles() []string {
	return append(append([]string{}, p.TestGoFiles...), p.XTestGoFiles...)
}

// Violation is one broken gate in one package.
type Violation struct {
	Package string // relative to the module, such as "internal/gateway"
	Problem string
}

func (v Violation) String() string { return v.Package + ": " + v.Problem }

// Load runs go list in dir and returns every package of the module there.
func Load(dir string) ([]Package, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w: %s", dir, err, stderr.String())
	}
	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p Package
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			return pkgs, nil
		} else if err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}
