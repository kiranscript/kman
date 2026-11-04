package gates

import (
	"slices"
	"strings"
)

// CheckModules reports every module from SPEC §2 that is missing or has no
// package doc comment, and every package directly under internal/ that is
// not a module.
func CheckModules(pkgs []Package) []Violation {
	var vs []Violation
	present := map[string]bool{}
	for _, p := range pkgs {
		name := module(p.Rel())
		if name == "" || p.Rel() != "internal/"+name {
			continue
		}
		present[name] = true
		switch {
		case !slices.Contains(Modules, name):
			vs = append(vs, Violation{p.Rel(), "is not a module in SPEC §2"})
		case !strings.HasPrefix(p.Doc, "Package "+name+" "):
			vs = append(vs, Violation{p.Rel(), "has no package doc comment"})
		}
	}
	for _, name := range Modules {
		if !present[name] {
			vs = append(vs, Violation{"internal/" + name, "is missing"})
		}
	}
	return vs
}
