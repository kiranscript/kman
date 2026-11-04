package e2e_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// kman is the path of the binary under test.
var kman string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "kman-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	kman = filepath.Join(dir, "kman")
	if runtime.GOOS == "windows" {
		kman += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", kman, "./cmd/kman")
	build.Dir = ".."
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build kman: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// runKman runs the binary with args and returns its standard output.
func runKman(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(kman, args...)
	var stderr []byte
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = ee.Stderr
	}
	if err != nil {
		t.Fatalf("kman %v: %v\n%s", args, err, stderr)
	}
	return string(out)
}
