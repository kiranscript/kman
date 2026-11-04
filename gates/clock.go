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

// A timeRule bans some functions of package time in some files.
type timeRule struct {
	banned         []string
	exemptSynctest bool   // files that import testing/synctest are exempt
	where, why     string // appended to each violation
}

var (
	// Only main reads the clock.
	clockReads = timeRule{
		banned: []string{"Now", "Since", "Until"},
		why:    "only main reads the clock; take the time as an argument",
	}
	// Tests under internal/ run timers on synctest's fake clock.
	timers = timeRule{
		banned:         []string{"Sleep", "After", "AfterFunc", "NewTimer", "NewTicker", "Tick"},
		exemptSynctest: true,
		where:          " outside testing/synctest",
		why:            "timer-driven tests run in a synctest bubble",
	}
)

// CheckClock enforces the clock seam of SPEC §17. Production code outside
// package main never reads the wall clock: time is an argument. Tests under
// internal/ that use timers import testing/synctest, so they run on a fake
// clock. Tests outside internal/, such as e2e, drive real processes and may
// wait on the real clock.
//
// The synctest check is per file: it trusts a file that imports
// testing/synctest to run its timers inside a bubble.
func CheckClock(pkgs []Package) []Violation {
	var vs []Violation
	for _, p := range pkgs {
		if p.Name != "main" {
			vs = append(vs, clockReads.check(p, p.GoFiles)...)
		}
		if module(p.Rel()) != "" {
			vs = append(vs, timers.check(p, p.TestFiles())...)
		}
	}
	return vs
}

// check reports every use of the rule's banned functions in files, and any
// dot-import of time, which would hide them.
func (r timeRule) check(p Package, files []string) []Violation {
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
			switch {
			case path == "time" && imp.Name != nil && imp.Name.Name == ".":
				line := fset.Position(imp.Pos()).Line
				vs = append(vs, Violation{p.Rel(), fmt.Sprintf("%s:%d dot-imports time, which hides clock reads from this gate", name, line)})
			case path == "time" && imp.Name != nil:
				timeName = imp.Name.Name
			case path == "time":
				timeName = "time"
			case path == "testing/synctest":
				synctest = true
			}
		}
		if timeName == "" || (r.exemptSynctest && synctest) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == timeName && slices.Contains(r.banned, sel.Sel.Name) {
				line := fset.Position(sel.Pos()).Line
				vs = append(vs, Violation{p.Rel(), fmt.Sprintf("%s:%d uses time.%s%s (%s)", name, line, sel.Sel.Name, r.where, r.why)})
			}
			return true
		})
	}
	return vs
}
