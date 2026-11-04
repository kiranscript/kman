package gates

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CheckTested reports every package under internal/ that has no test file,
// or whose test files hold no Test, Fuzz, Benchmark or Example function.
func CheckTested(pkgs []Package) []Violation {
	var vs []Violation
	for _, p := range pkgs {
		if module(p.Rel()) == "" {
			continue
		}
		files := append(append([]string{}, p.TestGoFiles...), p.XTestGoFiles...)
		if len(files) == 0 {
			vs = append(vs, Violation{p.Rel(), "has no tests"})
			continue
		}
		found, err := hasTestFunc(p.Dir, files)
		switch {
		case err != nil:
			vs = append(vs, Violation{p.Rel(), err.Error()})
		case !found:
			vs = append(vs, Violation{p.Rel(), "has no Test, Fuzz, Benchmark or Example function"})
		}
	}
	return vs
}

func hasTestFunc(dir string, files []string) (bool, error) {
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return false, err
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv == nil && isTestName(fn.Name.Name) {
				return true, nil
			}
		}
	}
	return false, nil
}

// isTestName reports whether name is one go test runs: a prefix followed by
// nothing or by a character that is not a lower-case letter.
func isTestName(name string) bool {
	for _, prefix := range []string{"Test", "Fuzz", "Benchmark", "Example"} {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		if rest == "" {
			return true
		}
		r, _ := utf8.DecodeRuneInString(rest)
		return !unicode.IsLower(r)
	}
	return false
}
