package gates

import (
	"bytes"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// networked are the standard packages through which code could reach the
// network, directly or by running something that does.
var networked = []string{"net", "net/http", "crypto/tls", "os/exec"}

// Deps returns everything pattern's package depends on, itself included,
// without its tests.
func Deps(dir, pattern string) ([]string, error) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pattern)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps %s in %s: %w: %s", pattern, dir, err, stderr.String())
	}
	return strings.Fields(string(out)), nil
}

// CheckOffline reports each dependency of a package that could reach the
// network.
func CheckOffline(pkg string, deps []string) []Violation {
	var vs []Violation
	for _, d := range deps {
		if slices.Contains(networked, d) {
			vs = append(vs, Violation{pkg, "depends on " + d + "; price data is never fetched at runtime (ADR 0007)"})
		}
	}
	return vs
}
