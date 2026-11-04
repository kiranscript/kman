package registry_test

import "testing"

// TestPlaceholder keeps registry under the no-untested-package gate until its
// interface tests land. Delete it when they do.
func TestPlaceholder(t *testing.T) {
	t.Skip("registry has no interface yet")
}
