//go:build unix

package vault

import "golang.org/x/sys/unix"

// noCoreDumps sets RLIMIT_CORE to zero, soft and hard, so no core dump can
// hold a secret.
func noCoreDumps() error {
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
