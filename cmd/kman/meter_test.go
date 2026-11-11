package main

import (
	"testing"
	"time"

	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/profiles"
)

// keySpend is how an operator sees a key's spend beside its caps, so it must put
// the key's own caps on the right period.
func TestAKeysSpendIsShownBesideItsCaps(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	m := meter.New(meter.Options{Prices: pricing.NewPrices(pricing.Builtin()), Counters: meter.NewMemory(), Now: func() time.Time { return now }})
	if _, err := m.Reserve(now, meter.Ask{KeyID: "abcd1234", Meters: true}); err != nil { // one unmetered request
		t.Fatal(err)
	}
	r, err := m.Reserve(now, meter.Ask{KeyID: "abcd1234", Meters: true, Metered: true, Model: "gpt-4o", RequestBytes: 400})
	if err != nil {
		t.Fatal(err)
	}
	m.Settle(now, r, meter.Outcome{Status: 200, Usage: profiles.Usage{Seen: true, InputTokens: 1_000_000}})

	got := keySpend(m, now, "abcd1234", `{"daily_cap_micro":5000000,"monthly_cap_micro":50000000}`)
	if got.Today.Period != "2026-11-11" || got.Month.Period != "2026-11" || got.Today.CapMicro != 5_000_000 || got.Month.CapMicro != 50_000_000 ||
		got.Today.CostMicro == 0 || got.Today.CostMicro != got.Month.CostMicro || got.Today.UnmeteredRequests != 1 || got.Today.Requests != 1 {
		t.Errorf("keySpend = %+v", got)
	}
	if got := keySpend(m, now, "abcd1234", `{}`); got.Today.CapMicro != 0 || got.Month.CapMicro != 0 {
		t.Errorf("a key with no caps shows %+v", got)
	}
	if got := keySpend(nil, now, "abcd1234", `{}`); got.Today.CostMicro != 0 || got.Today.Period != "" {
		t.Errorf("no meter shows %+v", got)
	}
}
