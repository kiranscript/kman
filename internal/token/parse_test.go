package token_test

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/token"
)

// Golden tokens, computed outside Go (Python's zlib.crc32 and hashlib) from
// the secret bytes 0x00, 0x01, … 0x1f, so they check the format, not this
// package's own arithmetic.
const (
	goldenVirtualKey = "kmn_Ab3dE6gH_003aUlTJC7tjlCTQj2uNU3MFagCXG9LRKRcwGkBIDlf1vMiGD"
	goldenChildKey   = "ek_kmn_Ab3dE6gH_003aUlTJC7tjlCTQj2uNU3MFagCXG9LRKRcwGkBIDlf4HHIoH"
	goldenAdminToken = "kma_003aUlTJC7tjlCTQj2uNU3MFagCXG9LRKRcwGkBIDlf0ey31A"
	// SHA-256 of the secret part, 003aUlTJ…BIDlf, shared by all three.
	goldenHash = "b0bd65ed3119037a4dcbf6b8138eb2f09de18490e98a773a68f9353f0c0331cd"
)

func hashOf(t *testing.T, hexHash string) token.Hash {
	t.Helper()
	b, err := hex.DecodeString(hexHash)
	if err != nil {
		t.Fatal(err)
	}
	return token.Hash(b)
}

func TestParseReadsTheKindAndKeyID(t *testing.T) {
	for _, tc := range []struct {
		in   string
		kind token.Kind
		id   token.KeyID
	}{
		{goldenVirtualKey, token.VirtualKey, "Ab3dE6gH"},
		{goldenChildKey, token.ChildKey, "Ab3dE6gH"},
		{goldenAdminToken, token.AdminToken, ""},
	} {
		got, err := token.Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%s): %v", tc.kind, err)
		}
		if got.Kind != tc.kind || got.ID != tc.id {
			t.Errorf("Parse(%s) = kind %v, id %q; want kind %v, id %q", tc.kind, got.Kind, got.ID, tc.kind, tc.id)
		}
		if want := hashOf(t, goldenHash); got.Hash != want {
			t.Errorf("Parse(%s).Hash = %x, want %x", tc.kind, got.Hash, want)
		}
	}
}

func TestParseRejectsBadTokensWithATypedError(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", token.ErrMalformed},
		{"unknown prefix", "sk-proj-" + goldenVirtualKey[4:], token.ErrMalformed},
		{"truncated", goldenVirtualKey[:len(goldenVirtualKey)-1], token.ErrMalformed},
		{"one character too many", goldenVirtualKey + "0", token.ErrMalformed},
		{"short key id", "kmn_Ab3dE6g_003aUlTJC7tjlCTQj2uNU3MFagCXG9LRKRcwGkBIDlf1vMiGD0", token.ErrMalformed},
		{"non-base62 in the secret", goldenAdminToken[:10] + "-" + goldenAdminToken[11:], token.ErrMalformed},
		{"non-base62 in the key id", "kmn_Ab3dE6g!" + goldenVirtualKey[12:], token.ErrMalformed},
		{"keman key without checksum", goldenVirtualKey[:len(goldenVirtualKey)-6], token.ErrMalformed},
		{"mistyped secret", goldenVirtualKey[:20] + "x" + goldenVirtualKey[21:], token.ErrChecksum},
		{"mistyped key id", "kmn_Ab3dE6gh" + goldenVirtualKey[12:], token.ErrChecksum},
		{"mistyped checksum", goldenAdminToken[:len(goldenAdminToken)-1] + "B", token.ErrChecksum},
		{"ek_ added to a virtual key", "ek_" + goldenVirtualKey, token.ErrChecksum},
		{"child key without ek_", goldenChildKey[len("ek_"):], token.ErrChecksum},
		{"virtual key as an admin token", "kma_" + goldenVirtualKey[len("kmn_Ab3dE6gH_"):], token.ErrChecksum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := token.Parse(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse error = %v, want %v", err, tc.want)
			}
			if got != (token.Token{}) {
				t.Errorf("Parse returned %+v alongside the error, want the zero Token", got)
			}
			if tc.in != "" && strings.Contains(err.Error(), tc.in[min(4, len(tc.in)):]) {
				t.Errorf("error %q repeats the token", err)
			}
		})
	}
}

// IsKeyShaped is how the gateway spots a virtual or child key among headers, before
// any checksum: by the prefixes ADR 0001 fixes, and by nothing else.
func TestIsKeyShapedIsTrueForTheKeyPrefixesAlone(t *testing.T) {
	for s, want := range map[string]bool{
		goldenVirtualKey:       true,
		goldenChildKey:         true,
		"kmn_":                 true, // no checksum here: Parse is what checks it
		"ek_kmn_x":             true,
		goldenAdminToken:       false, // an admin token is not a key an app may send
		"ek_other":             false,
		"sk-proj-abc":          false,
		"":                     false,
		" " + goldenVirtualKey: false,
	} {
		if got := token.IsKeyShaped(s); got != want {
			t.Errorf("IsKeyShaped(%q) = %v, want %v", s, got, want)
		}
	}
}
