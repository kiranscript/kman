package vault

import "testing"

// SetHarden replaces the process hardening for the length of one test.
func SetHarden(t testing.TB, f func() error) {
	old := harden
	harden = f
	t.Cleanup(func() { harden = old })
}
