//go:build unix

package vault_test

import (
	"runtime"
	"testing"

	"github.com/kirank55/kman/internal/vault"
	"golang.org/x/sys/unix"
)

// TestOpeningAVaultTurnsOffCoreDumpsAndDumpability uses the real hardening,
// so it leaves this test process hardened too.
func TestOpeningAVaultTurnsOffCoreDumpsAndDumpability(t *testing.T) {
	v, _, err := vault.Create(newFixedSource(t), vault.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v.Close()

	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CORE, &lim); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	if lim.Cur != 0 || lim.Max != 0 {
		t.Errorf("RLIMIT_CORE = %d/%d, want 0/0", lim.Cur, lim.Max)
	}
	if runtime.GOOS == "linux" {
		d, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("prctl: %v", err)
		}
		if d != 0 {
			t.Errorf("PR_GET_DUMPABLE = %d, want 0", d)
		}
	}
}
