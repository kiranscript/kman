package meter_test

import (
	"testing"
	"time"

	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/pricing"
)

// The table the tests price by. At these rates a token is a whole number of
// microdollars: an input token costs 1, an output token 2, so a reservation is
// easy to work out by hand.
//
//	m      input 1, output 2, up to 1000 tokens out
//	m-big  input 5, output 10, up to 1000 tokens out
//	cheap  input 1, output 1, up to 1000 tokens out, and 1 µ$ a request for web search
const (
	model    = "m"
	modelBig = "m-big"
)

func newTable(t testing.TB) *pricing.Prices {
	t.Helper()
	one := func(in, out int64) pricing.Entry {
		return pricing.Entry{Mode: "chat", MaxOutput: 1000, Schedule: pricing.Schedule{Rates: pricing.Rates{Input: in * 1_000_000, Output: out * 1_000_000}}}
	}
	tbl, err := pricing.New(pricing.Data{
		Schema: pricing.Schema, GeneratedAt: t0,
		Global: map[string]pricing.Entry{model: one(1, 2), modelBig: one(5, 10)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return pricing.NewPrices(tbl)
}

var t0 = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// newMeter is a meter over the memory counters and the table above.
func newMeter(t testing.TB, opts ...func(*meter.Options)) (*meter.Meter, *meter.Memory) {
	t.Helper()
	counters := meter.NewMemory()
	o := meter.Options{Prices: newTable(t), Counters: counters, Now: func() time.Time { return t0 }}
	for _, f := range opts {
		f(&o)
	}
	return meter.New(o), counters
}

// ask is a request for model m of a key on a metered upstream: 400 bytes of
// body (100 tokens in) and no limit of its own, so 1000 tokens out.
// Reserved: 100×1 + 1000×2 = 2100.
func ask(key string) meter.Ask {
	return meter.Ask{KeyID: key, Meters: true, Metered: true, Model: model, RequestBytes: 400}
}

const reserveM = 2100

// capped is ask with a cap of its own and the refusals a cap brings.
func capped(key string, daily, monthly int64) meter.Ask {
	a := ask(key)
	a.DailyCapMicro, a.MonthlyCapMicro, a.Strict = daily, monthly, true
	return a
}
