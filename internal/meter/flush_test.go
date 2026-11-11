package meter_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/meter"
)

func rowsOf(t *testing.T, c *meter.Memory) map[string]meter.Row {
	t.Helper()
	rows, err := c.Since(context.Background(), "0000-00-00")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]meter.Row{}
	for _, r := range rows {
		out[r.KeyID+" "+r.Day] = r
	}
	return out
}

func spendOnce(t *testing.T, m *meter.Meter, key string, at time.Time) {
	t.Helper()
	r, err := m.Reserve(at, ask(key))
	if err != nil {
		t.Fatal(err)
	}
	m.Settle(at, r, ok(200, usage()))
}

func TestFlushingAddsOnlyWhatIsNewSinceTheLastFlush(t *testing.T) {
	m, counters := newMeter(t)
	spendOnce(t, m, "k", t0)
	spendOnce(t, m, "k", t0)
	if err := m.Flush(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	spendOnce(t, m, "k", t0)
	m.Flush(context.Background(), t0)
	m.Flush(context.Background(), t0) // nothing new
	got := rowsOf(t, counters)["k 2026-11-11"]
	if got.CostMicro != 210 || got.Requests != 3 || got.EstimatedMicro != 0 {
		t.Errorf("the counters hold %+v, want three requests of 70", got)
	}
}

func TestFlushingKeepsEstimatesAndUnmeteredRequestsApart(t *testing.T) {
	m, counters := newMeter(t)
	r, _ := m.Reserve(t0, ask("k"))
	m.Settle(t0, r, meter.Outcome{Status: 200, Broken: true})
	unmetered := ask("k")
	unmetered.Metered = false
	m.Reserve(t0, unmetered)
	m.Flush(context.Background(), t0)
	got := rowsOf(t, counters)["k 2026-11-11"]
	if got.CostMicro != reserveM || got.EstimatedMicro != reserveM || got.Unmetered != 1 || got.Requests != 1 {
		t.Errorf("the counters hold %+v", got)
	}
}

// Spend that has not been flushed yet is still spend: a cap counts it.
func TestSpendNotYetFlushedStillCountsAgainstACap(t *testing.T) {
	m, _ := newMeter(t)
	spendOnce(t, m, "k", t0)
	if _, err := m.Reserve(t0, capped("k", 100, 0)); !errors.Is(err, meter.ErrCapReached) {
		t.Errorf("Reserve = %v", err)
	}
}

// The month so far is rebuilt at startup from the days the counters hold, so
// restarting neither empties a cap nor loses what a key has spent.
func TestTheMonthSoFarIsRebuiltAtStartup(t *testing.T) {
	counters := meter.NewMemory()
	first, _ := newMeter(t, func(o *meter.Options) { o.Counters = counters })
	spendOnce(t, first, "k", t0)
	spendOnce(t, first, "k", t0.AddDate(0, 0, -3))
	spendOnce(t, first, "k", t0.AddDate(0, -1, 0)) // last month: not this month's
	if err := first.Flush(context.Background(), t0); err != nil {
		t.Fatal(err)
	}

	second, _ := newMeter(t, func(o *meter.Options) { o.Counters = counters })
	if err := second.Load(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	got := second.Spend("k", t0)
	if got.Day.CostMicro != 70 || got.Month.CostMicro != 140 || got.Month.Requests != 2 {
		t.Errorf("after a restart: day %+v, month %+v; want 70 today and 140 this month", got.Day, got.Month)
	}
	// What was loaded is not flushed a second time.
	second.Flush(context.Background(), t0)
	if row := rowsOf(t, counters)["k 2026-11-11"]; row.CostMicro != 70 {
		t.Errorf("the counters hold %+v after a restart and a flush: spend was counted twice", row)
	}
	spendOnce(t, second, "k", t0)
	second.Flush(context.Background(), t0)
	if row := rowsOf(t, counters)["k 2026-11-11"]; row.CostMicro != 140 {
		t.Errorf("the counters hold %+v, want the new 70 added to the old", row)
	}
}

// failing is counters that refuse to be written while down is set.
type failing struct {
	*meter.Memory
	down atomic.Bool
}

func (f *failing) Add(ctx context.Context, rows []meter.Row) error {
	if f.down.Load() {
		return errors.New("the store is locked")
	}
	return f.Memory.Add(ctx, rows)
}

func TestAFailedFlushIsRetriedWithNothingLostOrCountedTwice(t *testing.T) {
	f := &failing{Memory: meter.NewMemory()}
	m, _ := newMeter(t, func(o *meter.Options) { o.Counters = f })
	spendOnce(t, m, "k", t0)
	f.down.Store(true)
	if err := m.Flush(context.Background(), t0); err == nil {
		t.Fatal("a flush into a store that is down reported success")
	}
	spendOnce(t, m, "k", t0)
	f.down.Store(false)
	if err := m.Flush(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	if got := rowsOf(t, f.Memory)["k 2026-11-11"]; got.CostMicro != 140 || got.Requests != 2 {
		t.Errorf("the counters hold %+v, want both of the requests, once", got)
	}
}

// SPEC §8: capped keys fail closed after 60 s of failed flushes. A key with no
// cap keeps going: it has nothing to be held to.
func TestCappedKeysFailClosedAfterAMinuteOfFailedFlushes(t *testing.T) {
	f := &failing{Memory: meter.NewMemory()}
	m, _ := newMeter(t, func(o *meter.Options) { o.Counters = f })
	spendOnce(t, m, "k", t0)
	f.down.Store(true)
	m.Flush(context.Background(), t0) // the first failure, at t0

	for _, tc := range []struct {
		after   time.Duration
		refused bool
	}{{0, false}, {59 * time.Second, false}, {60 * time.Second, true}, {5 * time.Minute, true}} {
		at := t0.Add(tc.after)
		_, err := m.Reserve(at, capped("k", 1_000_000, 0))
		if got := errors.Is(err, meter.ErrStoreUnavailable); got != tc.refused {
			t.Errorf("%v of failures: Reserve = %v, refused = %v, want %v", tc.after, err, got, tc.refused)
		}
		if _, err := m.Reserve(at, ask("k")); err != nil {
			t.Errorf("%v of failures: a key with no cap was refused: %v", tc.after, err)
		}
	}
	// The failures that continue don't push the deadline out.
	m.Flush(context.Background(), t0.Add(30*time.Second))
	if _, err := m.Reserve(t0.Add(61*time.Second), capped("k", 1_000_000, 0)); !errors.Is(err, meter.ErrStoreUnavailable) {
		t.Errorf("a later failed flush moved the deadline: %v", err)
	}
	// And one flush that works opens it again, though there is nothing to flush:
	// a refused request adds no spend, so nothing else would ever show the store is back.
	f.down.Store(false)
	if err := m.Flush(context.Background(), t0.Add(70*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve(t0.Add(71*time.Second), capped("k", 1_000_000, 0)); err != nil {
		t.Errorf("after the store came back: %v", err)
	}
}

func TestRunFlushesEveryFiveSecondsAndNotBefore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		counters := meter.NewMemory()
		now := time.Now()
		m := meter.New(meter.Options{Prices: newTable(t), Counters: counters, Now: time.Now})
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { m.Run(ctx); close(done) }()

		spendOnce(t, m, "k", now)
		time.Sleep(4900 * time.Millisecond)
		synctest.Wait()
		if n := len(rowsOf(t, counters)); n != 0 {
			t.Errorf("%d rows flushed before 5 s", n)
		}
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if got := rowsOf(t, counters)["k "+now.UTC().Format(time.DateOnly)]; got.CostMicro != 70 {
			t.Errorf("after 5 s the counters hold %+v", got)
		}
		stop()
		<-done
	})
}
