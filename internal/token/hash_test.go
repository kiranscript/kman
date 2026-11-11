package token_test

import (
	"testing"

	"github.com/kirank55/kman/internal/token"
)

func TestHashEqualMatchesOnlyTheSameSecret(t *testing.T) {
	stored := token.MintAdminToken()
	presented, err := token.Parse(stored.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	if !presented.Hash.Equal(stored.Hash) {
		t.Error("the hash of a presented token does not equal the stored hash")
	}
	other := token.MintAdminToken()
	if presented.Hash.Equal(other.Hash) {
		t.Error("hashes of two different secrets are equal")
	}
	last := stored.Hash
	last[len(last)-1] ^= 1
	if stored.Hash.Equal(last) {
		t.Error("hashes differing in the last bit are equal")
	}
}
