package gates

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
)

// Clock reads are banned outside package main; timers are banned in tests
// under internal/ unless the file runs them in a testing/synctest bubble.
var (
	clockReads = []string{"Now", "Since", "Until"}
	timers     = []string{"Sleep", "After", "AfterFunc", "NewTimer", "NewTicker", "Tick"}
)

// CheckClock enforces the clock seam of SPEC §17. Production code outside
// package main never reads the wall clock: time is an argument. Tests under
// internal/ that use timers import testing/synctest, so they run on a fake
// clock. Tests outside internal/, such as e2e, drive real processes and may
// wait on the real clock.
func CheckClock(pkgs []Package) []Violation {
	var vs []Violation
	for _, p := range pkgs {
		if p.Name != "main" {
			vs = append(vs, scan(p, p.GoFiles, clockReads, false,
				"only main reads the clock; take the time as an argument")...)
		}
		if module(p.Rel()) != "" {
			tests := append(append([]string{}, p.TestGoFiles...), p.XTestGoFiles...)
			vs = append(vs, scan(p, tests, timers, true,
				"timer-driven tests run in a synctest bubble")...)
		}
	}
	return vs
}

// scan reports every use of the named time functions in files. When
// exemptSynctest is set, a file that imports testing/synctest is skipped.
func scan(p Package, files, banned []string, exemptSynctest bool, why string) []Violation {
	var vs []Violation
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			vs = append(vs, Violation{p.Rel(), err.Error()})
			continue
		}
		timeName, synctest := "", false
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			switch path {
			case "time":
				timeName = "time"
				if imp.Name != nil {
					timeName = imp.Name.Name
				}
			case "testing/synctest":
				synctest = true
			}
		}
		if timeName == "" || (exemptSynctest && synctest) {
			continue
		}
		where := ""
		if exemptSynctest {
			where = " outside testing/synctest"
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != timeName || !slices.Contains(banned, sel.Sel.Name) {
				return true
			}
			line := fset.Position(sel.Pos()).Line
			vs = append(vs, Violation{p.Rel(), fmt.Sprintf("%s:%d uses time.%s%s (%s)", name, line, sel.Sel.Name, where, why)})
			return true
		})
	}
	return vs
}
