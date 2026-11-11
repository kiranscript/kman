//go:build unix && !linux

package vault

// hardenProcess stops core dumps. Outside Linux there is no PR_SET_DUMPABLE.
func hardenProcess() error { return noCoreDumps() }
