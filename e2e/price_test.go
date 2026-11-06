package e2e_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// priceListed is what price list --json says, as far as these tests look.
type priceListed struct {
	Overrides []struct {
		Upstream   string
		Model      string
		InputMicro int64 `json:"input_micro"`
		Hides      []string
	}
}

func (h *home) listPrices(t *testing.T) priceListed {
	t.Helper()
	out, _ := h.mustRun(t, "price", "list", "--model", "gpt-5.4", "--json")
	var got priceListed
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("price list --json printed %q: %v", out, err)
	}
	return got
}

func TestAPriceOverrideSurvivesARestartAndIsAudited(t *testing.T) {
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	h.addOpenAI(t)

	said, _ := h.mustRun(t, "price", "set", "gpt-5.4", "--input", "3", "--output", "9", "--max-output", "2000")
	if !strings.Contains(said, "hides the built-in entry in global") {
		t.Errorf("price set said:\n%s", said)
	}
	h.mustRun(t, "price", "set", "gpt-5.4-new", "--upstream", "openai", "--input", "1", "--output", "2", "--max-output", "10")
	h.mustRun(t, "upstream", "update", "openai", "--price-multiplier", "1.1")

	stopServer(t, s)
	s = h.serve(t)
	h.login(t, s)

	got := h.listPrices(t)
	if len(got.Overrides) != 2 {
		t.Fatalf("after a restart, overrides = %+v", got.Overrides)
	}
	install := got.Overrides[0]
	if install.Model != "gpt-5.4" || install.InputMicro != 3_000_000 || len(install.Hides) == 0 {
		t.Errorf("the install-wide override = %+v", install)
	}
	if out, _ := h.mustRun(t, "price", "list", "--model", "gpt-5.4-new"); !strings.Contains(out, "upstream openai") {
		t.Errorf("price list:\n%s", out)
	}
	if out, _ := h.mustRun(t, "upstream", "show", "openai"); !strings.Contains(out, "x1.1") {
		t.Errorf("upstream show:\n%s", out)
	}

	h.mustRun(t, "price", "rm", "gpt-5.4")
	h.mustRun(t, "upstream", "rm", "openai")
	if got := h.listPrices(t); len(got.Overrides) != 0 {
		t.Errorf("overrides that should be gone: %+v", got.Overrides)
	}
	audit, _ := h.mustRun(t, "audit")
	if !strings.Contains(audit, "price_override.set") || !strings.Contains(audit, "price_override.delete") {
		t.Errorf("audit:\n%s", audit)
	}
}
