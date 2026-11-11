package policy_test

import (
	"testing"

	"github.com/kirank55/kman/internal/policy"
)

func TestAnEmptyPolicyNormalizesToTheEmptyObjectAtTheCurrentVersion(t *testing.T) {
	for _, raw := range []string{`{}`, ` { } `, "{\n}"} {
		version, got, err := policy.Normalize([]byte(raw))
		if err != nil {
			t.Fatalf("Normalize(%q) = %v", raw, err)
		}
		if string(got) != `{}` || version != 1 {
			t.Errorf("Normalize(%q) = %d, %s; want 1, {}", raw, version, got)
		}
	}
}

// A field kman does not know is refused rather than kept: a mistyped limit
// that silently limits nothing is worse than an error.
func TestNormalizeRefusesWhatIsNotAPolicy(t *testing.T) {
	for _, raw := range []string{
		``, `null`, `[]`, `"text"`, `1`, `{`, `{} {}`, `{"spend_cap": 5}`,
	} {
		if _, got, err := policy.Normalize([]byte(raw)); err == nil {
			t.Errorf("Normalize(%q) = %s, want an error", raw, got)
		}
	}
}

func TestSpendCapsAreKeptAndReadBack(t *testing.T) {
	version, got, err := policy.Normalize([]byte(`{ "monthly_cap_micro": 50000000, "daily_cap_micro": 5000000 }`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"daily_cap_micro":5000000,"monthly_cap_micro":50000000}`; string(got) != want || version != 1 {
		t.Errorf("Normalize = %d, %s; want 1, %s", version, got, want)
	}
	p, err := policy.Parse(string(got))
	if err != nil {
		t.Fatal(err)
	}
	if p.DailyCapMicro != 5_000_000 || p.MonthlyCapMicro != 50_000_000 {
		t.Errorf("Parse = %+v", p)
	}
	if !p.RequiresMetering() {
		t.Error("a key with a cap must meter every request")
	}
}

func TestAKeyWithNoCapDoesNotRequireMetering(t *testing.T) {
	for _, raw := range []string{``, `{}`} {
		p, err := policy.Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) = %v", raw, err)
		}
		if p.RequiresMetering() || p.DailyCapMicro != 0 || p.MonthlyCapMicro != 0 {
			t.Errorf("Parse(%q) = %+v", raw, p)
		}
	}
}

// A cap that is not above zero would be read as no cap, or as a key that can
// spend nothing: neither is what anyone typing it means, so it is refused.
func TestACapMustBeAboveZero(t *testing.T) {
	for _, raw := range []string{
		`{"daily_cap_micro":0}`, `{"daily_cap_micro":-1}`, `{"monthly_cap_micro":0}`,
		`{"monthly_cap_micro":"5"}`, `{"daily_cap_micro":1.5}`,
	} {
		if _, got, err := policy.Normalize([]byte(raw)); err == nil {
			t.Errorf("Normalize(%q) = %s, want an error", raw, got)
		}
	}
}

func TestAMonthlyCapBelowTheDailyOneIsRefused(t *testing.T) {
	if _, got, err := policy.Normalize([]byte(`{"daily_cap_micro":9,"monthly_cap_micro":3}`)); err == nil {
		t.Errorf("Normalize = %s, want an error: a month holds a day", got)
	}
}
