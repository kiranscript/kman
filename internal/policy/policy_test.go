package policy_test

import "testing"

// TestPlaceholder keeps policy under the no-untested-package gate until its
// interface tests land. Delete it when they do.
func TestPlaceholder(t *testing.T) {
	t.Skip("policy has no interface yet")
}
