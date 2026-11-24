package policy_test

import (
	"strings"
	"testing"
	"time"

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

func TestSoftBudgetsAreKeptAndReadBack(t *testing.T) {
	version, got, err := policy.Normalize([]byte(`{"soft_budget_percents":[25,90],"monthly_budget_micro":40000000,"daily_budget_micro":2000000,"monthly_cap_micro":50000000}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"monthly_cap_micro":50000000,"daily_budget_micro":2000000,"monthly_budget_micro":40000000,"soft_budget_percents":[25,90]}`
	if string(got) != want || version != 1 {
		t.Errorf("Normalize = %d, %s; want 1, %s", version, got, want)
	}
	p, err := policy.Parse(string(got))
	if err != nil {
		t.Fatal(err)
	}
	if p.DailyBudgetMicro != 2_000_000 || p.MonthlyBudgetMicro != 40_000_000 || len(p.SoftBudgetPercents) != 2 || p.SoftBudgetPercents[1] != 90 {
		t.Errorf("Parse = %+v", p)
	}
}

// A soft budget warns and never refuses, so it does not make a key meter every
// request the way a cap does (SPEC §7).
func TestASoftBudgetWithoutACapDoesNotRequireMetering(t *testing.T) {
	p, err := policy.Parse(`{"monthly_budget_micro":40000000}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.RequiresMetering() {
		t.Error("a soft budget alone must not refuse unmetered requests")
	}
}

func TestNormalizeRefusesSoftBudgetsThatMakeNoSense(t *testing.T) {
	for _, raw := range []string{
		`{"daily_budget_micro":0}`, `{"monthly_budget_micro":-5}`,
		`{"daily_budget_micro":3000000,"daily_cap_micro":2000000}`,     // a budget above its cap warns after the refusals
		`{"monthly_budget_micro":3000000,"monthly_cap_micro":2000000}`, // likewise
		`{"daily_budget_micro":3000000,"monthly_budget_micro":2000000}`,
		`{"soft_budget_percents":[]}`,
		`{"soft_budget_percents":[80,50]}`, `{"soft_budget_percents":[50,50]}`,
		`{"soft_budget_percents":[0]}`, `{"soft_budget_percents":[1001]}`,
		`{"soft_budget_percents":[1,2,3,4,5,6,7,8,9,10,11]}`,
	} {
		if _, got, err := policy.Normalize([]byte(raw)); err == nil {
			t.Errorf("Normalize(%s) = %s, want an error", raw, got)
		}
	}
}

func TestALearningPeriodIsKeptInItsShortestFormAndReadBack(t *testing.T) {
	for raw, want := range map[string]string{
		`{"learning_period":"3d"}`:     `{"learning_period":"3d"}`,
		`{"learning_period":"72h"}`:    `{"learning_period":"3d"}`,
		`{"learning_period":"36h"}`:    `{"learning_period":"36h0m0s"}`,
		`{"learning_period":"1h30m"}`:  `{"learning_period":"1h30m0s"}`,
		`{"learning_period":"0s"}`:     `{"learning_period":"0s"}`,
		`{"learning_period":"0"}`:      `{"learning_period":"0s"}`,
		`{"learning_period":"90d"}`:    `{"learning_period":"90d"}`,
		` {"learning_period": "7d" } `: `{"learning_period":"7d"}`,
	} {
		_, got, err := policy.Normalize([]byte(raw))
		if err != nil {
			t.Errorf("Normalize(%s) = %v", raw, err)
			continue
		}
		if string(got) != want {
			t.Errorf("Normalize(%s) = %s, want %s", raw, got, want)
		}
		if _, err := policy.Parse(string(got)); err != nil {
			t.Errorf("the normalized form %s does not parse: %v", got, err)
		}
	}
	p, err := policy.Parse(`{"learning_period":"3d"}`)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := p.LearningPeriodOverride(); !ok || d != 72*time.Hour {
		t.Errorf("LearningPeriodOverride = %v, %v, want 72h", d, ok)
	}
	p, _ = policy.Parse(`{"learning_period":"0s"}`)
	if d, ok := p.LearningPeriodOverride(); !ok || d != 0 {
		t.Errorf("a learning period of 0 is an override of none: %v, %v", d, ok)
	}
	p, _ = policy.Parse(`{}`)
	if _, ok := p.LearningPeriodOverride(); ok {
		t.Error("a policy that sets none overrides nothing")
	}
}

func TestNormalizeRefusesALearningPeriodThatMakesNoSense(t *testing.T) {
	for _, raw := range []string{
		`{"learning_period":""}`, `{"learning_period":"soon"}`, `{"learning_period":"-1h"}`, `{"learning_period":"-1d"}`,
		`{"learning_period":"91d"}`, `{"learning_period":"2200h"}`, `{"learning_period":7}`, `{"learning_period":"1.5d"}`,
		`{"learning_period":null}`,
	} {
		if _, got, err := policy.Normalize([]byte(raw)); err == nil {
			t.Errorf("Normalize(%s) = %s, want an error", raw, got)
		}
	}
}

func TestARuleIsOnUnlessAKeyTurnsItOff(t *testing.T) {
	p, err := policy.Parse(`{"detect":{"new_country":false,"new_asn":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	for rule, want := range map[string]bool{"new_country": false, "new_asn": true, "new_network": true, "new_client": true} {
		if got := p.RuleEnabled(rule); got != want {
			t.Errorf("RuleEnabled(%s) = %v, want %v", rule, got, want)
		}
	}
	_, got, err := policy.Normalize([]byte(`{"detect":{"new_client":false,"new_asn":true,"new_country":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	// A rule that is on is the default, so it is not kept.
	if want := `{"detect":{"new_client":false,"new_country":false}}`; string(got) != want {
		t.Errorf("Normalize = %s, want %s", got, want)
	}
	_, got, err = policy.Normalize([]byte(`{"detect":{"new_asn":true}}`))
	if err != nil || string(got) != `{}` {
		t.Errorf("Normalize(a rule left on) = %s, %v, want {}", got, err)
	}
}

func TestNormalizeRefusesARuleThatDoesNotExistAndTheCanaryRule(t *testing.T) {
	for raw, mention := range map[string]string{
		`{"detect":{"new_planet":false}}`: "new_country",
		`{"detect":{"canary":false}}`:     "canary",
		`{"detect":{"new_country":"no"}}`: "",
		`{"detect":{"new_country":null}}`: "",
		`{"detect":[]}`:                   "",
		`{"detect":{"":false}}`:           "",
	} {
		_, got, err := policy.Normalize([]byte(raw))
		if err == nil {
			t.Errorf("Normalize(%s) = %s, want an error", raw, got)
			continue
		}
		if mention != "" && !strings.Contains(err.Error(), mention) {
			t.Errorf("Normalize(%s) = %v, want it to mention %q", raw, err, mention)
		}
	}
}

// The default policy turns every location rule on, so a key made before they
// existed is watched by them.
func TestAPolicyThatSaysNothingKeepsEveryRuleOn(t *testing.T) {
	p, err := policy.Parse(``)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range policy.LocationRules {
		if !p.RuleEnabled(rule) {
			t.Errorf("%s is off by default", rule)
		}
	}
}
