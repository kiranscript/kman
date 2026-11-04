//go:build unix

package vault_test

import (
	"testing"

	"github.com/kirank55/kman/internal/vault"
	"golang.org/x/sys/unix"
)

// TestOpeningAVaultTurnsOffCoreDumpsAndDumpability checks RLIMIT_CORE, and
// PR_SET_DUMPABLE where there is one. It uses the real hardening,
// so it leaves this test process hardened too.
func TestOpeningAVaultTurnsOffCoreDumpsAndDumpability(t *testing.T) {
	v, _, err := vault.Create(newFixedSource(t), testDEKID, vault.Options{})
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
	checkNotDumpable(t)
}
