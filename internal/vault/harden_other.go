//go:build !unix

package vault

// hardenProcess does nothing where there is no RLIMIT_CORE or
// PR_SET_DUMPABLE, such as Windows. Go's default crash path there does not
// call Windows Error Reporting.
func hardenProcess() error { return nil }
