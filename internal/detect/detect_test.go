package detect_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/detect"
	"github.com/kirank55/kman/internal/token"
)

func mint(t *testing.T, kind token.Kind) token.Minted {
	t.Helper()
	id := token.NewKeyID()
	var m token.Minted
	var err error
	switch kind {
	case token.VirtualKey:
		m, err = token.MintVirtualKey(id)
	case token.ChildKey:
		m, err = token.MintChildKey(id)
	default:
		m = token.MintAdminToken()
	}
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func scan(t *testing.T, text string) []detect.Finding {
	t.Helper()
	got, err := detect.Scan("test.env", strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestScanFindsAVirtualKeyAndNamesItsKeyIDAndPlace(t *testing.T) {
	m := mint(t, token.VirtualKey)
	id := m.Reveal()[len("kmn_") : len("kmn_")+8]
	got := scan(t, "# config\nOPENAI_API_KEY="+m.Reveal()+"\n")
	if len(got) != 1 {
		t.Fatalf("Scan found %d, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Source != "test.env" || f.Line != 2 || f.Column != len("OPENAI_API_KEY=")+1 {
		t.Errorf("found at %s:%d:%d, want test.env:2:16", f.Source, f.Line, f.Column)
	}
	if f.Kind != "virtual key" || f.KeyID != id || f.Severity != detect.High || f.Label != "virtual key" {
		t.Errorf("finding = %+v, want a high virtual key with id %s", f, id)
	}
}

func TestScanReportsAChildKeyAtLowerSeverityLabelledShortLived(t *testing.T) {
	m := mint(t, token.ChildKey)
	got := scan(t, `{"client_secret": "`+m.Reveal()+`"}`)
	if len(got) != 1 {
		t.Fatalf("Scan found %d, want 1", len(got))
	}
	if got[0].Kind != "child key" || got[0].Severity != detect.Low || got[0].Label != "child key (short-lived)" {
		t.Errorf("finding = %+v, want a low child key labelled \"child key (short-lived)\"", got[0])
	}
}

func TestScanConfirmsByChecksumAndDropsWhatOnlyLooksLikeAKey(t *testing.T) {
	m := mint(t, token.VirtualKey).Reveal()
	// Same shape, wrong checksum: the last character is changed.
	last := m[len(m)-1]
	bad := m[:len(m)-1] + string(map[bool]byte{true: 'A', false: 'B'}[last != 'A'])
	if got := scan(t, "key="+bad); len(got) != 0 {
		t.Errorf("Scan reported a match with a bad checksum: %+v", got)
	}
	// A child key with its ek_ removed fails the checksum, which covers ek_.
	c := mint(t, token.ChildKey).Reveal()
	if got := scan(t, "key="+c[len("ek_"):]); len(got) != 0 {
		t.Errorf("Scan reported a child key without its ek_: %+v", got)
	}
}

func TestScanIgnoresAdminTokens(t *testing.T) {
	if got := scan(t, mint(t, token.AdminToken).Reveal()); len(got) != 0 {
		t.Errorf("Scan reported an admin token: %+v", got)
	}
}

func TestScanFindsEveryKeyOnEveryLine(t *testing.T) {
	a, b := mint(t, token.VirtualKey).Reveal(), mint(t, token.ChildKey).Reveal()
	got := scan(t, a+" "+b+"\r\nnothing\r\n"+a)
	if len(got) != 3 {
		t.Fatalf("Scan found %d, want 3: %+v", len(got), got)
	}
	if got[0].Line != 1 || got[1].Line != 1 || got[2].Line != 3 || got[1].Column != len(a)+2 {
		t.Errorf("positions = %d:%d, %d:%d, %d:%d", got[0].Line, got[0].Column, got[1].Line, got[1].Column, got[2].Line, got[2].Column)
	}
}

func TestAFindingNeverHoldsTheSecret(t *testing.T) {
	m := mint(t, token.VirtualKey).Reveal()
	secret := m[len("kmn_")+9:][:43]
	for _, f := range scan(t, m) {
		if s := strings.Join([]string{f.Source, f.Kind, f.KeyID, f.Label, f.Redacted}, "|"); strings.Contains(s, secret[:10]) {
			t.Errorf("finding %+v holds the secret", f)
		}
		if f.Redacted != "kmn_"+f.KeyID+"_…" {
			t.Errorf("Redacted = %q, want the prefix and key id only", f.Redacted)
		}
	}
}

// A file is read in blocks, so a key can straddle two. It must be found once,
// wherever it falls, and a word that merely ends in kmn_ must not match.
func TestScanFindsAKeyWhereverItFallsInALargeInput(t *testing.T) {
	m := mint(t, token.ChildKey).Reveal()
	for _, pad := range []int{0, 1, 63, 64, 65, 66, 67, 1 << 16, 1<<16 - 30, 1<<16 - 64, 1<<16 - 66, 3<<16 - 40, 1 << 20} {
		text := strings.Repeat("x ", pad/2) + strings.Repeat("\n", pad%2) + m + " tail"
		got := scan(t, text)
		if len(got) != 1 {
			t.Errorf("pad %d: Scan found %d, want 1", pad, len(got))
		}
	}
	long := strings.Repeat("a", 200000)
	if got := scan(t, long+m+long); len(got) != 0 {
		t.Errorf("a key inside a longer word was reported: %+v", got)
	}
}

func TestScanSeesAKeyAfterAVeryLongLineAndCountsLinesAcrossBlocks(t *testing.T) {
	m := mint(t, token.VirtualKey).Reveal()
	text := strings.Repeat("y", 300000) + "\n" + strings.Repeat("z\n", 40000) + " " + m
	got := scan(t, text)
	if len(got) != 1 || got[0].Line != 40002 || got[0].Column != 2 {
		t.Errorf("found %+v, want one at line 40002 column 2", got)
	}
}
