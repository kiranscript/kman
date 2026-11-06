package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

func TestPriceListShowsTheTableAndItsAge(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "price", "list")
	if !strings.Contains(out, "Price data generated 2026-11-01, 3 days old.\n") {
		t.Errorf("no age:\n%s", out)
	}
	for _, line := range []string{
		"global   gpt-5.4              $2.50  $15     $0.25   128000      over 272000 tokens: $5/$22.50; tiers: flex, priority; web_search $0.01",
		"groq     openai/gpt-oss-120b  $0.15  $0.60           65536       reviewed",
		"global   gpt-live-1                                              $0.05/min, $0.004/text input",
	} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("lacks %q in:\n%s", line, out)
		}
	}
	if strings.Contains(out, "upgrade") {
		t.Errorf("asks for an upgrade at 3 days:\n%s", out)
	}
}

func TestPriceListFilters(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "price", "list", "--preset", "groq")
	if !strings.Contains(out, "gpt-oss-120b") || strings.Contains(out, "gpt-5.4") {
		t.Errorf("--preset groq:\n%s", out)
	}
	out = in.kman(t, "price", "list", "--model", "gpt-5")
	if strings.Contains(out, "gpt-oss-120b") || !strings.Contains(out, "gpt-5.4") {
		t.Errorf("--model gpt-5:\n%s", out)
	}
}

func TestPriceListJSON(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	var got admin.PriceList
	in.kmanJSON(t, &got, "price", "list")
	if got.Status.AgeDays != 3 || got.Status.Stale || len(got.Models) != 3 || got.Models[0].Model != "gpt-5.4" || got.Models[0].InputMicro != 2500000 {
		t.Errorf("--json = %+v", got)
	}
	if !got.Status.GeneratedAt.Equal(priceTableAt) {
		t.Errorf("generated_at = %v", got.Status.GeneratedAt)
	}
}

func TestPriceListPastSixtyDaysAsksForAnUpgrade(t *testing.T) {
	in := newInstall(t, withPricesGenerated(t0.AddDate(0, 0, -73))).loggedIn(t)
	if out := in.kman(t, "price", "list"); !strings.Contains(out, "price data is 73 days old, upgrade kman") {
		t.Errorf("no upgrade note:\n%s", out)
	}
}

func TestPriceRefusesAnUnknownSubcommand(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	if r := in.run(t, nil, "", "price", "frob"); r.code != 1 || !strings.Contains(r.stderr, "usage: kman price") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestStatusSummaryShowsTheServersPriceAge(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.status(t, admin.PriceDataStatus{GeneratedAt: t0.AddDate(-1, 0, 0), AgeDays: 365, Stale: true, Notice: "not from the server"})
	if !strings.Contains(out, "Price data generated 2026-11-01, 3 days old.") {
		t.Errorf("status:\n%s", out)
	}
}

func TestStatusSummaryAsksForAnUpgradePastSixtyDays(t *testing.T) {
	in := newInstall(t, withPricesGenerated(t0.AddDate(0, 0, -61))).loggedIn(t)
	out := in.status(t, admin.PriceDataStatus{})
	if !strings.Contains(out, "price data is 61 days old, upgrade kman") {
		t.Errorf("status:\n%s", out)
	}
}

func TestStatusSummaryWithNoServerShowsThisBinarysTable(t *testing.T) {
	in := newInstall(t) // never logged in
	out := in.status(t, admin.PriceDataStatus{GeneratedAt: t0.Add(-49 * time.Hour), AgeDays: 2})
	if !strings.Contains(out, "Price data generated 2026-11-02, 2 days old.") {
		t.Errorf("status:\n%s", out)
	}
}
