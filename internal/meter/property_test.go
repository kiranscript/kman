package meter_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/profiles"
)

// The properties of SPEC §17 hold under concurrency: a capped key never ends up
// more than one request over its cap, and every reservation is settled or
// released, so none is left holding room. Run under -race.

type kind int

const (
	settleReported kind = iota
	settleBroken
	settleError
	releaseIt
	settleLate
)

func TestSpendNeverPassesACapByMoreThanOneRequest(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		const (
			workers  = 32
			perEach  = 200
			dailyCap = 50_000
		)
		m, counters := newMeter(t)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var admitted, refused int
		var worstOvershoot int64
		for w := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewPCG(seed, uint64(w)))
				for range perEach {
					a := capped("k", dailyCap, 0)
					a.RequestBytes = int64(rng.IntN(2000))
					a.MaxTokens = int64(1 + rng.IntN(1500))
					r, err := m.Reserve(t0, a)
					if err != nil {
						if !errors.Is(err, meter.ErrCapReached) {
							t.Errorf("Reserve = %v", err)
						}
						mu.Lock()
						refused++
						mu.Unlock()
						continue
					}
					mu.Lock()
					admitted++
					mu.Unlock()
					// What a request reserved is an upper bound for most of them: the
					// tokens it uses are within its limits. The odd one is over, as when
					// the bytes ÷ 4 estimate is low.
					switch k := kind(rng.IntN(5)); k {
					case settleReported:
						in, out := int64(rng.IntN(int(a.RequestBytes/4)+1)), int64(rng.IntN(int(min(a.MaxTokens, 1000))+1))
						m.Settle(t0, r, ok(200, profiles.Usage{Seen: true, InputTokens: in, OutputTokens: out}))
					case settleBroken:
						m.Settle(t0, r, meter.Outcome{Status: 200, Broken: true})
					case settleError:
						m.Settle(t0, r, ok(429, profiles.Usage{}))
					case releaseIt:
						m.Release(r)
					case settleLate:
						m.Settle(t0, r, ok(200, profiles.Usage{Seen: true, InputTokens: int64(a.RequestBytes), OutputTokens: 100})) // more input than reserved
					}
					got := m.Spend("k", t0).Day
					mu.Lock()
					worstOvershoot = max(worstOvershoot, got.CostMicro+got.ReservedMicro-dailyCap)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		got := m.Spend("k", t0)
		if got.Day.ReservedMicro != 0 || got.Month.ReservedMicro != 0 {
			t.Fatalf("seed %d: %d reserved after every request ended", seed, got.Day.ReservedMicro)
		}
		// The most one request can cost: 4× its bytes of input at the late settle (2000 → 2000) and 100 out.
		const oneRequest = 2000*1 + 1000*2
		if over := got.Day.CostMicro - dailyCap; over > oneRequest {
			t.Errorf("seed %d: spent %d against a cap of %d: over by %d, more than one request (%d)", seed, got.Day.CostMicro, dailyCap, over, oneRequest)
		}
		if worstOvershoot > oneRequest {
			t.Errorf("seed %d: spend plus reservations reached %d over the cap", seed, worstOvershoot)
		}
		if admitted == 0 || refused == 0 {
			t.Errorf("seed %d: %d admitted, %d refused: the cap should bite partway", seed, admitted, refused)
		}
		if err := m.Flush(context.Background(), t0); err != nil {
			t.Fatal(err)
		}
		if row := rowsOf(t, counters)["k 2026-11-11"]; row.CostMicro != got.Day.CostMicro {
			t.Errorf("seed %d: the counters hold %d, the meter %d", seed, row.CostMicro, got.Day.CostMicro)
		}
	}
}

// Settling, releasing, flushing and rolling over the day all at once, with no
// cap to hide a mistake behind: the books must come out even.
func TestTheBooksBalanceWhateverIsGoingOn(t *testing.T) {
	m, counters := newMeter(t)
	var wg sync.WaitGroup
	var cost, requests int64
	var mu sync.Mutex
	stop := make(chan struct{})
	go func() { // a flusher
		for {
			select {
			case <-stop:
				return
			default:
				m.Flush(context.Background(), t0)
			}
		}
	}()
	for w := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 300 {
				at := t0.Add(time.Duration(i%3) * 24 * time.Hour) // three days: a rollover each time
				r, err := m.Reserve(at, ask("k"))
				if err != nil {
					t.Errorf("Reserve = %v", err)
					return
				}
				c := m.Settle(at, r, ok(200, usage()))
				mu.Lock()
				cost += c.Micro
				requests++
				mu.Unlock()
				_ = w
			}
		}()
	}
	wg.Wait()
	close(stop)
	if err := m.Flush(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	var flushed, flushedRequests int64
	for _, row := range rowsOf(t, counters) {
		flushed += row.CostMicro
		flushedRequests += row.Requests
	}
	if flushed != cost || flushedRequests != requests {
		t.Errorf("the counters hold %d over %d requests; %d over %d were charged", flushed, flushedRequests, cost, requests)
	}
	if got := m.Spend("k", t0.Add(48*time.Hour)); got.Day.ReservedMicro != 0 || got.Month.ReservedMicro != 0 {
		t.Errorf("reservations left open: %+v", got)
	}
}
