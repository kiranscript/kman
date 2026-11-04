package token_test

import (
	"testing"

	"github.com/kirank55/kman/internal/token"
)

func TestScanPatternIsThePublishedOne(t *testing.T) {
	// Copied from ADR 0001 and SPEC §4: third-party scanners rely on it.
	const published = `\b(?:ek_)?kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}\b`
	if token.ScanPattern != published {
		t.Errorf("ScanPattern = %q, want %q", token.ScanPattern, published)
	}
	if token.Scanner.String() != published {
		t.Errorf("Scanner = %q, want %q", token.Scanner, published)
	}
}

func TestScannerFindsMintedKeysOfEveryKind(t *testing.T) {
	for _, m := range mintEach(t) {
		s := m.Reveal()
		for _, text := range []string{
			s,
			"OPENAI_API_KEY=" + s + "\n",
			`{"apiKey": "` + s + `"}`,
			"export KMAN_KEY='" + s + "'",
		} {
			got := token.Scanner.FindString(text)
			if m.Kind == token.AdminToken {
				// The published pattern is for keys an app holds; an admin
				// token never matches it.
				if got != "" {
					t.Errorf("Scanner matched %q in an admin token", got)
				}
				continue
			}
			if got != s {
				t.Errorf("Scanner in %q found %q, want the whole %v", text, got, m.Kind)
			}
		}
	}
}

func TestScannerMatchesAChildKeyWhole(t *testing.T) {
	// \bkmn_ alone would miss a child key or match only its tail.
	got := token.Scanner.FindAllString("a "+goldenChildKey+" b "+goldenVirtualKey, -1)
	if len(got) != 2 || got[0] != goldenChildKey || got[1] != goldenVirtualKey {
		t.Errorf("Scanner found %q, want the child key and the virtual key whole", got)
	}
}
