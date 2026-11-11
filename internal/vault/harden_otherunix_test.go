//go:build unix && !linux

package vault_test

import "testing"

// checkNotDumpable has nothing to check: only Linux has PR_SET_DUMPABLE.
func checkNotDumpable(*testing.T) {}
