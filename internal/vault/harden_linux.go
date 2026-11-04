package vault

import "golang.org/x/sys/unix"

// hardenProcess stops core dumps and makes the process non-dumpable, which
// also stops same-user ptrace and reads of /proc/<pid>/mem and environ.
func hardenProcess() error {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return err
	}
	return noCoreDumps()
}
