package token

// MintWithSecret mints a token from the given secret bits, so tests can
// check minting against golden tokens.
func MintWithSecret(kind Kind, id KeyID, secret [32]byte) (Minted, error) {
	return mint(kind, id, secret)
}
