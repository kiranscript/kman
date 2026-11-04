package token

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Minted is a newly minted token. Its plaintext is shown once, at creation,
// and only through Reveal: printing a Minted shows its kind and key id only.
type Minted struct {
	Token
	plaintext string
}

// Reveal returns the token itself, to show once to whoever asked for it.
// Store m.Hash, never this.
func (m Minted) Reveal() string { return m.plaintext }

// NewKeyID returns a random key id: 8 base62 characters.
func NewKeyID() KeyID {
	var b [idLen]byte
	randomBase62(b[:])
	return KeyID(b[:])
}

// MintVirtualKey mints a virtual key with a fresh secret for the key id.
// Rotation mints with the key's existing id (ADR 0002).
func MintVirtualKey(id KeyID) (Minted, error) {
	return mint(VirtualKey, id, randomSecret())
}

// MintChildKey mints a child key with a fresh secret for the key id.
func MintChildKey(id KeyID) (Minted, error) {
	return mint(ChildKey, id, randomSecret())
}

// MintAdminToken mints an admin token with a fresh secret.
func MintAdminToken() Minted {
	m, _ := mint(AdminToken, "", randomSecret()) // cannot fail without a key id
	return m
}

func randomSecret() (secret [32]byte) {
	rand.Read(secret[:]) // never fails (crypto/rand, Go 1.24+)
	return secret
}

// mint builds the token for the kind, key id and 256-bit secret.
func mint(kind Kind, id KeyID, secret [32]byte) (Minted, error) {
	var b strings.Builder
	b.Grow(maxBody + crcLen)
	switch kind {
	case VirtualKey, ChildKey:
		if len(id) != idLen || !isBase62(string(id)) {
			return Minted{}, ErrMalformed
		}
		if kind == ChildKey {
			b.WriteString(childPrefix)
		} else {
			b.WriteString(virtualPrefix)
		}
		b.WriteString(string(id))
		b.WriteByte('_')
	case AdminToken:
		if id != "" {
			return Minted{}, ErrMalformed
		}
		b.WriteString(adminPrefix)
	default:
		return Minted{}, ErrMalformed
	}
	var s [secretLen]byte
	encode(s[:], secret[:])
	b.Write(s[:])
	var c [crcLen]byte
	encodeUint(c[:], uint64(checksum(b.String())))
	b.Write(c[:])
	return Minted{
		Token:     Token{Kind: kind, ID: id, Hash: sha256.Sum256(s[:])},
		plaintext: b.String(),
	}, nil
}

// String names the token by kind and key id, without its secret.
func (m Minted) String() string {
	if m.ID == "" {
		return m.Kind.String()
	}
	return m.Kind.String() + " " + string(m.ID)
}

// Format prints m as String does, for every verb, so no fmt verb shows the
// plaintext.
func (m Minted) Format(f fmt.State, _ rune) { io.WriteString(f, m.String()) }

// LogValue logs m as String does.
func (m Minted) LogValue() slog.Value { return slog.StringValue(m.String()) }
