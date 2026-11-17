package meter_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/profiles"
)

func ok(status int, u profiles.Usage) meter.Outcome { return meter.Outcome{Status: status, Usage: u} }

// usage is what a response reports: 50 tokens in, 10 out, which costs 50 + 20.
func usage() profiles.Usage {
	return profiles.Usage{Seen: true, InputTokens: 50, OutputTokens: 10}
}

func mustReserve(t *testing.T, m *meter.Meter, a meter.Ask) *meter.Reservation {
	t.Helper()
	r, err := m.Reserve(t0, a)
	if err != nil {
		t.Fatalf("Reserve = %v", err)
	}
	return r
}

func TestAResponseIsChargedWhatItReportedNotWhatWasReserved(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	if got := m.Spend("k", t0).Day; got.ReservedMicro != reserveM || got.CostMicro != 0 {
		t.Fatalf("while open: %+v, want %d reserved", got, reserveM)
	}
	c := m.Settle(t0, r, ok(200, usage()))
	if c.Micro != 70 || c.Estimated {
		t.Errorf("Settle = %+v, want 70 µ$ reported", c)
	}
	day := m.Spend("k", t0).Day
	if day.CostMicro != 70 || day.ReservedMicro != 0 || day.EstimatedMicro != 0 || day.Requests != 1 {
		t.Errorf("Spend = %+v", day)
	}
}

// F2, keman's second cap bypass reversed: its 429s and 5xx charged the whole
// reservation, so a flood of refusals used up a cap. A refusal with no usage is free.
func TestRegression_F2_AnErrorWithNoUsageChargesNothing(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500, 502, 503, 529} {
		m, _ := newMeter(t)
		r := mustReserve(t, m, capped("k", 10_000, 0))
		if c := m.Settle(t0, r, ok(status, profiles.Usage{})); c.Micro != 0 || c.Estimated {
			t.Errorf("status %d: Settle = %+v, want nothing charged", status, c)
		}
		if got := m.Spend("k", t0).Day; got.CostMicro != 0 || got.ReservedMicro != 0 {
			t.Errorf("status %d: Spend = %+v", status, got)
		}
	}
}

func TestAnErrorThatReportsUsageIsChargedForIt(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	if c := m.Settle(t0, r, ok(500, usage())); c.Micro != 70 {
		t.Errorf("Settle = %+v, want the 70 it reported, whatever the status", c)
	}
}

func TestAResponseThatNeverCameCostsNothing(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	if c := m.Settle(t0, r, meter.Outcome{}); c.Micro != 0 {
		t.Errorf("Settle = %+v: an upstream that did not answer charged nothing", c)
	}
	if got := m.Spend("k", t0).Day; got.ReservedMicro != 0 || got.Requests != 0 {
		t.Errorf("Spend = %+v", got)
	}
}

func TestABrokenStreamOrAGoneClientIsChargedTheWholeReservationAsAnEstimate(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	c := m.Settle(t0, r, meter.Outcome{Status: 200, Broken: true})
	if c.Micro != reserveM || !c.Estimated {
		t.Errorf("Settle = %+v, want %d, estimated", c, reserveM)
	}
	if got := m.Spend("k", t0).Day; got.CostMicro != reserveM || got.EstimatedMicro != reserveM {
		t.Errorf("Spend = %+v", got)
	}
}

// What it had read of a broken stream is not the whole: the cap can't be left
// to a stream that cut itself off before its usage.
func TestABrokenStreamIsChargedTheReservationEvenIfItReportedSome(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	if c := m.Settle(t0, r, meter.Outcome{Status: 200, Broken: true, Usage: usage()}); c.Micro != reserveM || !c.Estimated {
		t.Errorf("Settle = %+v", c)
	}
}

// The decision made for #41: a 2xx on a metered upstream that reported no usage
// ran, and may have cost anything (an image, speech), so it is charged the
// reservation as an estimate. Otherwise a cap could be walked past by a route
// that reports nothing.
func TestASuccessWithNoUsageIsChargedTheReservationAsAnEstimate(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	if c := m.Settle(t0, r, ok(200, profiles.Usage{})); c.Micro != reserveM || !c.Estimated {
		t.Errorf("Settle = %+v, want %d, estimated", c, reserveM)
	}
}

func TestARouteThatIsFreeIsChargedNothingWithOrWithoutUsage(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	if c := m.Settle(t0, r, meter.Outcome{Status: 200, Free: true}); c.Micro != 0 || c.Estimated {
		t.Errorf("Settle = %+v", c)
	}
}

func TestAResponseIsChargedAtTheModelTierAndCostItReports(t *testing.T) {
	m, _ := newMeter(t)
	// It asked for m and was served m-big, which costs 5 and 10: 50×5 + 10×10.
	r := mustReserve(t, m, ask("k"))
	u := usage()
	u.Model = modelBig
	if c := m.Settle(t0, r, ok(200, u)); c.Micro != 350 || c.Model != modelBig {
		t.Errorf("Settle = %+v, want 350 at %s", c, modelBig)
	}
	// A model with no price falls back to the one asked for.
	r = mustReserve(t, m, ask("k"))
	u.Model = "a-model-nobody-priced"
	if c := m.Settle(t0, r, ok(200, u)); c.Micro != 70 || c.Model != model {
		t.Errorf("Settle = %+v, want 70 at %s", c, model)
	}
	// A cost the provider reports is final.
	r = mustReserve(t, m, ask("k"))
	u = profiles.Usage{Seen: true, ReportedCost: true, ReportedMicro: 1234, InputTokens: 50}
	if c := m.Settle(t0, r, ok(200, u)); c.Micro != 1234 || c.Estimated {
		t.Errorf("Settle = %+v, want the reported 1234", c)
	}
}

func TestAnAdmittedRequestMayCostMoreThanItReservedButOnlyThatOne(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, capped("k", 3000, 0))
	big := profiles.Usage{Seen: true, InputTokens: 5000, OutputTokens: 100} // 5200: more than the 2100 reserved
	m.Settle(t0, r, ok(200, big))
	if _, err := m.Reserve(t0, capped("k", 3000, 0)); !errors.Is(err, meter.ErrCapReached) {
		t.Errorf("Reserve after overshooting the cap = %v, want ErrCapReached", err)
	}
}

func TestSettleTakesEffectOnceAndAnUnsettledReservationCanBeReleased(t *testing.T) {
	m, _ := newMeter(t)
	r := mustReserve(t, m, ask("k"))
	m.Settle(t0, r, ok(200, usage()))
	m.Settle(t0, r, ok(200, usage()))
	m.Release(r)
	if got := m.Spend("k", t0).Day; got.CostMicro != 70 || got.Requests != 1 || got.ReservedMicro != 0 {
		t.Errorf("Spend = %+v, want one request of 70", got)
	}
	r = mustReserve(t, m, ask("k"))
	m.Release(r)
	m.Settle(t0, r, ok(200, usage()))
	if got := m.Spend("k", t0).Day; got.CostMicro != 70 || got.ReservedMicro != 0 {
		t.Errorf("Spend = %+v: a released reservation settled too", got)
	}
	m.Settle(t0, nil, ok(200, usage())) // a request nothing was reserved for
}

func TestAdmissionNeedsCommittedPlusOpenPlusNewToFitTheCap(t *testing.T) {
	m, _ := newMeter(t)
	cap := int64(2*reserveM + 100)
	a := capped("k", cap, 0)
	r1 := mustReserve(t, m, a)
	r2 := mustReserve(t, m, a)
	if _, err := m.Reserve(t0, a); !errors.Is(err, meter.ErrCapReached) {
		t.Fatalf("a third reservation = %v: two are open, and a third would pass the cap", err)
	}
	m.Settle(t0, r1, ok(200, usage())) // 70 committed, one still open
	r3, err := m.Reserve(t0, a)
	if err != nil {
		t.Fatalf("after one settled: %v (70 + %d open + %d new fits %d)", err, reserveM, reserveM, cap)
	}
	m.Release(r2)
	m.Release(r3)
}

func TestTheCapIsInclusive(t *testing.T) {
	m, _ := newMeter(t)
	if _, err := m.Reserve(t0, capped("k", reserveM, 0)); err != nil {
		t.Errorf("a reservation equal to the cap = %v", err)
	}
	if _, err := m.Reserve(t0, capped("j", reserveM-1, 0)); !errors.Is(err, meter.ErrCapReached) {
		t.Errorf("a reservation a µ$ over the cap = %v", err)
	}
	// The refusal leaves nothing held.
	if got := m.Spend("j", t0).Day.ReservedMicro; got != 0 {
		t.Errorf("a refused request left %d reserved", got)
	}
}

func TestTheMonthlyCapHoldsWhenTheDailyOneIsRoomy(t *testing.T) {
	m, _ := newMeter(t)
	a := capped("k", 10*reserveM, reserveM+10)
	r := mustReserve(t, m, a)
	if _, err := m.Reserve(t0, a); !errors.Is(err, meter.ErrCapReached) {
		t.Fatalf("second = %v", err)
	}
	// The monthly refusal gave back what it took of the day.
	if got := m.Spend("k", t0).Day.ReservedMicro; got != reserveM {
		t.Errorf("day reserved = %d, want only the first's %d", got, reserveM)
	}
	m.Release(r)
}

// F3, keman's third bypass: caps added late started from zero, because counters
// were kept only for keys with a cap. Every key has spend.
func TestRegression_F3_ACapAddedLateCountsWhatWasSpentBefore(t *testing.T) {
	m, _ := newMeter(t)
	for range 3 {
		m.Settle(t0, mustReserve(t, m, ask("k")), ok(200, usage())) // no cap: 210 spent
	}
	if got := m.Spend("k", t0); got.Day.CostMicro != 210 || got.Month.CostMicro != 210 {
		t.Fatalf("an uncapped key's spend = %+v", got)
	}
	// A cap of 300 is added: 210 spent, so a reservation of 2100 can't fit.
	if _, err := m.Reserve(t0, capped("k", 300, 0)); !errors.Is(err, meter.ErrCapReached) {
		t.Errorf("Reserve = %v: the cap should count the 210 already spent", err)
	}
	// And a cap a request fits under, counting what is spent.
	if _, err := m.Reserve(t0, capped("k", 210+reserveM, 0)); err != nil {
		t.Errorf("Reserve under a roomy cap = %v", err)
	}
}

func TestEachKeyHasItsOwnSpend(t *testing.T) {
	m, _ := newMeter(t)
	m.Settle(t0, mustReserve(t, m, ask("a")), ok(200, usage()))
	if got := m.Spend("b", t0); got.Day.CostMicro != 0 || got.Month.CostMicro != 0 {
		t.Errorf("another key's spend = %+v", got)
	}
}

func TestTheDayAndMonthRollOverAtMidnightInTheSpendTimezone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	m, _ := newMeter(t, func(o *meter.Options) { o.Location = berlin })
	// 22:30 UTC on 30 Nov is 23:30 in Berlin, and 23:30 UTC is already 1 Dec there.
	late := time.Date(2026, 11, 30, 22, 30, 0, 0, time.UTC)
	after := time.Date(2026, 11, 30, 23, 30, 0, 0, time.UTC)
	r, _ := m.Reserve(late, ask("k"))
	m.Settle(late, r, ok(200, usage()))
	if got := m.Spend("k", late); got.Day.Label != "2026-11-30" || got.Month.Label != "2026-11" || got.Day.CostMicro != 70 {
		t.Errorf("before midnight: %+v", got)
	}
	got := m.Spend("k", after)
	if got.Day.Label != "2026-12-01" || got.Month.Label != "2026-12" || got.Day.CostMicro != 0 || got.Month.CostMicro != 0 {
		t.Errorf("after midnight in Berlin: %+v, want a new day and month with nothing in them", got)
	}
	r, _ = m.Reserve(after, ask("k"))
	m.Settle(after, r, ok(200, usage()))
	if got := m.Spend("k", after); got.Day.CostMicro != 70 || got.Month.CostMicro != 70 {
		t.Errorf("spend in the new month = %+v", got)
	}
}

func TestTheDayResetsButTheMonthCarriesOn(t *testing.T) {
	m, _ := newMeter(t)
	r, _ := m.Reserve(t0, ask("k"))
	m.Settle(t0, r, ok(200, usage()))
	next := t0.AddDate(0, 0, 1)
	r, _ = m.Reserve(next, ask("k"))
	m.Settle(next, r, ok(200, usage()))
	got := m.Spend("k", next)
	if got.Day.CostMicro != 70 || got.Month.CostMicro != 140 {
		t.Errorf("Spend = day %d, month %d; want 70 and 140", got.Day.CostMicro, got.Month.CostMicro)
	}
	// A reservation open across midnight is returned to the day it was taken in.
	r, _ = m.Reserve(next, ask("k"))
	m.Settle(next.AddDate(0, 0, 1), r, ok(200, usage()))
	if got := m.Spend("k", next); got.Day.ReservedMicro != 0 || got.Month.ReservedMicro != 0 {
		t.Errorf("a reservation open across midnight was left held: %+v", got)
	}
}

// An unpriced model's cost is unknown, which is not a cost of zero: the charge
// says so, for the event of the request. A cost the response reported is known.
func TestAChargeForAModelWithNoPriceIsUnknownUnlessTheResponseReportedACost(t *testing.T) {
	m, _ := newMeter(t)
	a := ask("k")
	a.Model = "no-such-model"
	r, err := m.Reserve(t0, a)
	if err != nil {
		t.Fatal(err)
	}
	if c := m.Settle(t0, r, ok(200, usage())); !c.Unknown || c.Micro != 0 {
		t.Errorf("charge %+v, want an unknown cost", c)
	}
	r, _ = m.Reserve(t0, a)
	reported := meter.Outcome{Status: 200, Usage: profiles.Usage{Seen: true, ReportedCost: true, ReportedMicro: 42}}
	if c := m.Settle(t0, r, reported); c.Unknown || c.Micro != 42 {
		t.Errorf("charge %+v, want the reported 42", c)
	}
}
