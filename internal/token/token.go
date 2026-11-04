// Package token holds the token types shared across kman: the kind of a
// token and the key id inside a virtual key. It is a leaf: it imports no
// other internal package, so any module may import it (ADR 0006).
//
// The token formats themselves are fixed by ADR 0001.
package token

import "strconv"

// Kind is what a token is, as far as its format tells. A canary key is a
// VirtualKey: a key's kind beyond this lives on the server (ADR 0001).
type Kind int

const (
	// VirtualKey is a kmn_ token an app sends in place of a credential.
	VirtualKey Kind = iota + 1
	// ChildKey is a short-lived ek_kmn_ token minted from a virtual key.
	ChildKey
	// AdminToken is a kma_ token an operator uses to manage kman.
	AdminToken
)

func (k Kind) String() string {
	switch k {
	case VirtualKey:
		return "virtual key"
	case ChildKey:
		return "child key"
	case AdminToken:
		return "admin token"
	}
	return "token.Kind(" + strconv.Itoa(int(k)) + ")"
}

// KeyID is the non-secret handle inside a virtual key or child key, used to
// look the key up and to name it safely in logs, alerts and UIs.
type KeyID string
