package token_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/token"
)

func counting() (b [32]byte) {
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func TestMintMatchesTheGoldenTokens(t *testing.T) {
	for _, tc := range []struct {
		kind token.Kind
		id   token.KeyID
		want string
	}{
		{token.VirtualKey, "Ab3dE6gH", goldenVirtualKey},
		{token.ChildKey, "Ab3dE6gH", goldenChildKey},
		{token.AdminToken, "", goldenAdminToken},
	} {
		m, err := token.MintWithSecret(tc.kind, tc.id, counting())
		if err != nil {
			t.Fatalf("mint %v: %v", tc.kind, err)
		}
		if got := m.Reveal(); got != tc.want {
			t.Errorf("mint %v = %q, want %q", tc.kind, got, tc.want)
		}
		if m.Hash != hashOf(t, goldenHash) {
			t.Errorf("mint %v hash = %x, want %s", tc.kind, m.Hash, goldenHash)
		}
	}
}

func TestMintEncodesTheSecretEdgeValues(t *testing.T) {
	var zero, ones [32]byte
	for i := range ones {
		ones[i] = 0xff
	}
	for _, tc := range []struct {
		kind   token.Kind
		id     token.KeyID
		secret [32]byte
		want   string // from Python, as the golden tokens
	}{
		{token.VirtualKey, "00000000", zero, "kmn_00000000_00000000000000000000000000000000000000000002A1tSs"},
		{token.AdminToken, "", ones, "kma_yhjskwdA6OZ1AL1YmHWZWm8LLG7HjnuCA2j5rOw8Xp12yZicu"},
	} {
		m, err := token.MintWithSecret(tc.kind, tc.id, tc.secret)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Reveal(); got != tc.want {
			t.Errorf("mint = %q, want %q", got, tc.want)
		}
	}
}

// Anchored forms of the ADR 0001 formats, written out independently of the
// package's constants.
var formats = map[token.Kind]*regexp.Regexp{
	token.VirtualKey: regexp.MustCompile(`^kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}$`),
	token.ChildKey:   regexp.MustCompile(`^ek_kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}$`),
	token.AdminToken: regexp.MustCompile(`^kma_[0-9A-Za-z]{49}$`),
}

func mintEach(t *testing.T) []token.Minted {
	t.Helper()
	id := token.NewKeyID()
	vk, err := token.MintVirtualKey(id)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := token.MintChildKey(id)
	if err != nil {
		t.Fatal(err)
	}
	return []token.Minted{vk, ck, token.MintAdminToken()}
}

func TestMintedTokensParseBack(t *testing.T) {
	for _, m := range mintEach(t) {
		s := m.Reveal()
		if !formats[m.Kind].MatchString(s) {
			t.Errorf("minted %v %q does not match its format", m.Kind, s)
		}
		got, err := token.Parse(s)
		if err != nil {
			t.Fatalf("Parse(minted %v): %v", m.Kind, err)
		}
		if got != m.Token {
			t.Errorf("Parse(minted %v) = %+v, want %+v", m.Kind, got, m.Token)
		}
	}
}

func TestMintDrawsAFreshSecretEachTime(t *testing.T) {
	id := token.NewKeyID()
	a, _ := token.MintVirtualKey(id)
	b, _ := token.MintVirtualKey(id)
	if a.Reveal() == b.Reveal() || a.Hash == b.Hash {
		t.Errorf("two mints for key id %q gave the same secret", id)
	}
	if token.NewKeyID() == token.NewKeyID() {
		t.Error("two new key ids are equal")
	}
}

func TestMintRefusesABadKeyID(t *testing.T) {
	for _, id := range []token.KeyID{"", "Ab3dE6g", "Ab3dE6gH0", "Ab3dE6g_", "Ab3dE6g!"} {
		if _, err := token.MintVirtualKey(id); !errors.Is(err, token.ErrMalformed) {
			t.Errorf("MintVirtualKey(%q) error = %v, want ErrMalformed", id, err)
		}
		if _, err := token.MintChildKey(id); !errors.Is(err, token.ErrMalformed) {
			t.Errorf("MintChildKey(%q) error = %v, want ErrMalformed", id, err)
		}
	}
}

func TestPrintingAMintedTokenHidesTheSecret(t *testing.T) {
	m, err := token.MintWithSecret(token.VirtualKey, "Ab3dE6gH", counting())
	if err != nil {
		t.Fatal(err)
	}
	secret := goldenVirtualKey[len("kmn_Ab3dE6gH_"):]
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		for _, v := range []any{m, &m, []token.Minted{m}, struct{ M token.Minted }{m}} {
			got := fmt.Sprintf(verb, v)
			if strings.Contains(got, secret[:8]) || strings.Contains(got, fmt.Sprintf("%x", secret[:8])) {
				t.Errorf("Sprintf(%s, %T) = %q shows the secret", verb, v, got)
			}
		}
	}
	if got, want := fmt.Sprint(m), "virtual key Ab3dE6gH"; got != want {
		t.Errorf("Sprint(minted) = %q, want %q", got, want)
	}
}

func TestLoggingAMintedTokenHidesTheSecret(t *testing.T) {
	m, err := token.MintWithSecret(token.ChildKey, "Ab3dE6gH", counting())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("minted", "token", m)
	if got := buf.String(); strings.Contains(got, goldenChildKey[len("ek_kmn_Ab3dE6gH_"):][:8]) {
		t.Errorf("log line %q shows the secret", got)
	} else if !strings.Contains(got, `"token":"child key Ab3dE6gH"`) {
		t.Errorf("log line %q does not name the token", got)
	}
}
