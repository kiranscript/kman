package vault

import "fmt"

// harden is hardenProcess, replaced in tests that check when it runs.
var harden = hardenProcess

// hardenFirst hardens the process unless the operator opted out with
// --allow-debug. It runs before any key source is read.
func hardenFirst(opts Options) error {
	if opts.AllowDebug {
		return nil
	}
	if err := harden(); err != nil {
		return fmt.Errorf("harden the process before reading the master key (--allow-debug skips this): %w", err)
	}
	return nil
}
