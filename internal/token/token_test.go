package token_test

import (
	"testing"

	"github.com/kirank55/kman/internal/token"
)

func TestKindNamesUseTheGlossaryTerms(t *testing.T) {
	for kind, want := range map[token.Kind]string{
		token.VirtualKey: "virtual key",
		token.ChildKey:   "child key",
		token.AdminToken: "admin token",
		token.Kind(0):    "token.Kind(0)",
		token.Kind(9):    "token.Kind(9)",
	} {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(kind), got, want)
		}
	}
}
