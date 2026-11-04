package vault_test

import (
	"testing"

	"golang.org/x/sys/unix"
)

func checkNotDumpable(t *testing.T) {
	t.Helper()
	d, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("prctl: %v", err)
	}
	if d != 0 {
		t.Errorf("PR_GET_DUMPABLE = %d, want 0", d)
	}
}
