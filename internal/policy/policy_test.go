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
