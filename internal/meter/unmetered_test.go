package meter_test

import (
	"errors"
	"testing"

	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/profiles"
)

// F1, keman's first cap bypass: a body with no top-level model skipped the caps
// and the model allowlist. A key that must be metered never forwards a request
// that can't be.
func TestRegression_F1_AStrictKeyNeverForwardsARequestWithNoModel(t *testing.T) {
	m, _ := newMeter(t)
	for _, tc := range []struct {
		name string
		edit func(*meter.Ask)
	}{
		{"no model in the body", func(a *meter.Ask) { a.Metered, a.Model = false, "" }},
		{"an upstream that isn't metered at all", func(a *meter.Ask) { a.Meters, a.Metered, a.Model = false, false, "" }},
	} {
		a := capped("k", 1_000_000, 0)
		tc.edit(&a)
		r, err := m.Reserve(t0, a)
		if !errors.Is(err, meter.ErrUnmetered) || r != nil {
			t.Errorf("%s: Reserve = %v, %v; want ErrUnmetered", tc.name, r, err)
		}
	}
	// A model allowlist alone also makes a key strict, with no cap.
	a := ask("k")
	a.Strict, a.Metered = true, false
	if _, err := m.Reserve(t0, a); !errors.Is(err, meter.ErrUnmetered) {
		t.Errorf("a strict key with no cap: %v", err)
	}
}

func TestAnUnpricedModelIsRefusedOnAStrictKeyAndNamesTheModel(t *testing.T) {
	m, _ := newMeter(t)
	a := capped("k", 1_000_000, 0)
	a.Model = "gpt-9-unpriced"
	_, err := m.Reserve(t0, a)
	var unknown *meter.UnknownPriceError
	if !errors.Is(err, meter.ErrUnknownPrice) || !errors.As(err, &unknown) || unknown.Model != "gpt-9-unpriced" {
		t.Errorf("Reserve = %v, want an unknown price for the model", err)
	}
	if got := m.Spend("k", t0).Day; got.Unmetered != 0 || got.ReservedMicro != 0 {
		t.Errorf("a refusal left %+v", got)
	}
}

// SPEC §8: other keys forward what can't be metered and count it, as unmetered,
// never as $0.
func TestAKeyWithNoCapForwardsWhatCannotBeMeteredAndCountsIt(t *testing.T) {
	m, _ := newMeter(t)
	a := ask("k")
	a.Metered, a.Model = false, ""
	if r, err := m.Reserve(t0, a); err != nil || r != nil {
		t.Fatalf("Reserve = %v, %v; want it forwarded with nothing reserved", r, err)
	}
	a = ask("k")
	a.Model = "gpt-9-unpriced"
	r, err := m.Reserve(t0, a)
	if err != nil {
		t.Fatalf("Reserve of an unpriced model = %v", err)
	}
	m.Settle(t0, r, ok(200, usage())) // tokens it can't price
	got := m.Spend("k", t0)
	if got.Day.Unmetered != 2 || got.Month.Unmetered != 2 || got.Day.CostMicro != 0 || got.Day.Requests != 0 {
		t.Errorf("Spend = %+v: two unmetered requests, and no cost to show as $0", got.Day)
	}
}

// A cost the provider reports needs no price, so an unpriced model can still be charged.
func TestAnUnpricedModelIsChargedACostTheProviderReports(t *testing.T) {
	m, _ := newMeter(t)
	a := ask("k")
	a.Model = "gpt-9-unpriced"
	r, _ := m.Reserve(t0, a)
	if c := m.Settle(t0, r, ok(200, profiles.Usage{Seen: true, ReportedCost: true, ReportedMicro: 99})); c.Micro != 99 {
		t.Errorf("Settle = %+v", c)
	}
	if got := m.Spend("k", t0).Day; got.CostMicro != 99 || got.Unmetered != 0 || got.Requests != 1 {
		t.Errorf("Spend = %+v", got)
	}
}

func TestAnUnmeteredUpstreamHasNoSpendAndRefusesOnlyAStrictKey(t *testing.T) {
	m, _ := newMeter(t)
	a := meter.Ask{KeyID: "k", Meters: false}
	if r, err := m.Reserve(t0, a); err != nil || r != nil {
		t.Errorf("Reserve = %v, %v", r, err)
	}
	if got := m.Spend("k", t0); got.Day.Unmetered != 0 || got.Day.CostMicro != 0 {
		t.Errorf("a generic upstream's key has spend %+v", got)
	}
	a.Strict = true
	if _, err := m.Reserve(t0, a); !errors.Is(err, meter.ErrUnmetered) {
		t.Errorf("a capped key on a generic upstream: %v", err)
	}
}

// An upstream's own overrides are found by its id, its built-in prices by its
// preset, and its multiplier scales them: all three reach the table (notes 34 and 39).
func TestTheTableIsAskedByTheUpstreamsIdPresetAndMultiplier(t *testing.T) {
	base := newTable(t)
	if err := base.SetOverrides([]pricing.Override{
		{UpstreamID: "up_a", Model: model, MaxOutput: 1000, Rates: pricing.Rates{Input: 10_000_000, Output: 10_000_000}},
	}); err != nil {
		t.Fatal(err)
	}
	m, _ := newMeter(t, func(o *meter.Options) { o.Prices = base })
	// On up_a, m costs 10 an input token and 10 an output token: 100×10 + 1000×10.
	a := ask("k")
	a.UpstreamID = "up_a"
	if got := reserved(t, m, a); got != 11_000 {
		t.Errorf("on the upstream with the override: reserved %d, want 11000", got)
	}
	// On any other, the built-in price: 100 + 2000.
	a.UpstreamID = "up_b"
	if got := reserved(t, m, a); got != reserveM {
		t.Errorf("on another upstream: reserved %d, want %d", got, reserveM)
	}
	// The multiplier of the upstream scales a built-in price, not its own override.
	a.MultiplierPPM = 1_500_000
	if got := reserved(t, m, a); got != 3150 {
		t.Errorf("with a ×1.5 multiplier: reserved %d, want 3150", got)
	}
	a.UpstreamID = "up_a"
	if got := reserved(t, m, a); got != 11_000 {
		t.Errorf("a multiplier changed an upstream's own price: reserved %d", got)
	}
}

// reserved is what Ask reserves for a key nothing else is using.
func reserved(t *testing.T, m *meter.Meter, a meter.Ask) int64 {
	t.Helper()
	a.KeyID = "probe-" + a.UpstreamID + string(rune('a'+a.MultiplierPPM%7))
	r, err := m.Reserve(t0, a)
	if err != nil {
		t.Fatal(err)
	}
	got := m.Spend(a.KeyID, t0).Day.ReservedMicro
	m.Release(r)
	return got
}

func TestAnUnpricedModelOnAnUpstreamsOverrideIsPricedByThatOverride(t *testing.T) {
	prices := newTable(t)
	if err := prices.SetOverrides([]pricing.Override{{UpstreamID: "up_local", Model: "llama-local", MaxOutput: 100, Rates: pricing.Rates{}}}); err != nil {
		t.Fatal(err)
	}
	m, _ := newMeter(t, func(o *meter.Options) { o.Prices = prices })
	a := capped("k", 1_000_000, 0)
	a.UpstreamID, a.Model = "up_local", "llama-local"
	r, err := m.Reserve(t0, a)
	if err != nil {
		t.Fatalf("a model with a zero price override = %v", err)
	}
	if c := m.Settle(t0, r, ok(200, usage())); c.Micro != 0 {
		t.Errorf("Settle = %+v, a local model is free", c)
	}
}
