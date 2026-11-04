package gates

import (
	"fmt"
	"slices"
	"strings"
)

// Modules lists the packages SPEC §2 allows directly under internal/: the
// fifteen modules plus token, a leaf package of token types that anything
// may import.
var Modules = []string{
	"gateway", "profiles", "policy", "limiter", "meter", "pricing",
	"registry", "vault", "store", "events", "detect", "alerts", "sessions",
	"admin", "adminapi", "dashboard", "cli",
	"token",
}

var transports = []string{"adminapi", "dashboard", "cli"}

// allowed is the closed import list for the modules SPEC §2 constrains.
// token is added to every list, since it is the leaf anything may import.
var allowed = map[string][]string{
	"adminapi":  {"admin"},
	"dashboard": {"admin"},
	"cli":       {"admin"},
	"admin":     {"store", "registry", "vault", "events", "sessions"},
	"gateway":   {"registry", "policy", "limiter", "meter", "vault", "profiles", "events", "sessions"},
	"token":     {},
}

// module returns the module under internal/ that a module-relative package
// path belongs to, such as "gateway" for "internal/gateway/sse", or "" when
// the path is outside internal/.
func module(rel string) string {
	rest, ok := strings.CutPrefix(rel, "internal/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	return name
}

// CheckImports enforces the import rule of SPEC §2 on every package under
// internal/. Only production imports count: tests may reach further.
// Packages outside internal/, such as cmd/kman, are the composition root and
// may import anything.
func CheckImports(pkgs []Package) []Violation {
	var vs []Violation
	for _, p := range pkgs {
		from := module(p.Rel())
		if from == "" {
			continue
		}
		for _, imp := range p.Imports {
			if p.Module == nil || !strings.HasPrefix(imp, p.Module.Path+"/") {
				continue
			}
			to := module(strings.TrimPrefix(imp, p.Module.Path+"/"))
			if to == "" || to == from {
				continue
			}
			if problem := importProblem(from, to); problem != "" {
				vs = append(vs, Violation{p.Rel(), fmt.Sprintf("imports internal/%s (%s)", to, problem)})
			}
		}
	}
	return vs
}

func importProblem(from, to string) string {
	switch {
	case from == "token":
		return "token is a leaf and imports no internal package"
	case slices.Contains(transports, to):
		return "nothing may import a transport"
	case to == "token":
		return ""
	case slices.Contains(transports, from):
		if !slices.Contains(allowed[from], to) {
			return "a transport may import only admin and token"
		}
	case allowed[from] != nil:
		if !slices.Contains(allowed[from], to) {
			return fmt.Sprintf("%s may import only %s and token", from, strings.Join(allowed[from], ", "))
		}
	}
	return ""
}
