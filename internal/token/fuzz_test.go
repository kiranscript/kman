package token_test

import (
	"errors"
	"testing"

	"github.com/kirank55/kman/internal/token"
)

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		goldenVirtualKey, goldenChildKey, goldenAdminToken,
		"ek_" + goldenVirtualKey, goldenChildKey[3:], goldenVirtualKey[:30],
		"", "kmn_", "ek_kmn_", "kma_", "kmn_Ab3dE6gH_", "kmn_zzzzzzzz_" + goldenAdminToken[4:],
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := token.Parse(s)
		if err != nil {
			if !errors.Is(err, token.ErrMalformed) && !errors.Is(err, token.ErrChecksum) {
				t.Fatalf("Parse error %v is neither ErrMalformed nor ErrChecksum", err)
			}
			return
		}
		re, ok := formats[got.Kind]
		if !ok || !re.MatchString(s) {
			t.Fatalf("Parse accepted %q as a %v, which does not match that format", s, got.Kind)
		}
		if got.Kind != token.AdminToken && string(got.ID) != s[len(s)-58:len(s)-50] {
			t.Fatalf("Parse(%q) key id = %q", s, got.ID)
		}
	})
}

func FuzzMintRoundTrip(f *testing.F) {
	f.Add(uint8(token.VirtualKey), "Ab3dE6gH", []byte{0, 1, 2})
	f.Add(uint8(token.ChildKey), "zzzzzzzz", []byte{0xff})
	f.Add(uint8(token.AdminToken), "", []byte{})
	f.Fuzz(func(t *testing.T, kind uint8, id string, secretBytes []byte) {
		var secret [32]byte
		copy(secret[:], secretBytes)
		k := token.Kind(kind)
		m, err := token.MintWithSecret(k, token.KeyID(id), secret)
		if err != nil {
			if valid(k, id) {
				t.Fatalf("mint %v with key id %q: %v", k, id, err)
			}
			return
		}
		if !valid(k, id) {
			t.Fatalf("mint %v accepted key id %q", k, id)
		}
		s := m.Reveal()
		got, err := token.Parse(s)
		if err != nil {
			t.Fatalf("Parse(minted %q): %v", s, err)
		}
		if got != m.Token || got.Kind != k || got.ID != token.KeyID(id) {
			t.Fatalf("Parse(minted %q) = %+v, want %+v", s, got, m.Token)
		}
		if k != token.AdminToken && token.Scanner.FindString(s) != s {
			t.Fatalf("Scanner does not match minted %q whole", s)
		}
	})
}

// valid reports whether a key id fits the kind, written out independently of
// the package.
func valid(k token.Kind, id string) bool {
	switch k {
	case token.AdminToken:
		return id == ""
	case token.VirtualKey, token.ChildKey:
		if len(id) != 8 {
			return false
		}
		for _, c := range []byte(id) {
			if !('0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z') {
				return false
			}
		}
		return true
	}
	return false
}
