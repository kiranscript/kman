package meter

import (
	"context"
	"fmt"
	"time"
)

// flushTimeout bounds one flush, so that a store that has hung can't keep
// shutdown waiting, and counts as failed.
const flushTimeout = 10 * time.Second

// Load reads this month's spend from the counters, so that a restart neither
// empties a cap nor loses what a key has spent. It is for a Meter that hasn't
// taken a request yet.
func (m *Meter) Load(ctx context.Context, now time.Time) error {
	_, month := labels(now, m.opts.Location)
	rows, err := m.opts.Counters.Since(ctx, month+"-01")
	if err != nil {
		return fmt.Errorf("meter: read this month's spend: %w", err)
	}
	for _, r := range rows {
		a := m.account(r.KeyID)
		a.mu.Lock()
		ds := a.days[r.Day]
		if ds == nil {
			ds = &dayState{b: &bucket{label: r.Day}}
			a.days[r.Day] = ds
		}
		monthLabel := r.Day[:len("2006-01")]
		mb := a.months[monthLabel]
		if mb == nil {
			mb = &bucket{label: monthLabel}
			a.months[monthLabel] = mb
		}
		for _, b := range [2]*bucket{ds.b, mb} {
			b.held.Add(r.CostMicro)
			b.cost.Add(r.CostMicro)
			b.estimated.Add(r.EstimatedMicro)
			b.requests.Add(r.Requests)
			b.unmetered.Add(r.Unmetered)
		}
		// It's in the counters already.
		ds.flushedCost += r.CostMicro
		ds.flushedEst += r.EstimatedMicro
		ds.flushedReq += r.Requests
		ds.flushedUnmetered += r.Unmetered
		a.mu.Unlock()
	}
	return nil
}

// Run flushes spend to the counters every FlushEvery until ctx ends. It doesn't
// flush as it stops: the caller does, with Flush, once the requests that were
// in flight have settled.
func (m *Meter) Run(ctx context.Context) {
	t := time.NewTicker(m.opts.FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.Flush(ctx, m.opts.Now()); err != nil && ctx.Err() == nil {
				m.opts.Log.Error("spend could not be flushed", "err", err)
			}
		}
	}
}

// pending is what one day of one key has gained since it was last flushed.
type pending struct {
	state *dayState
	row   Row
}

// Flush adds what has been spent since the last flush to the counters, as
// increments, and notes at now whether it worked. Capped keys are refused once
// flushes have failed for FailClosedAfter. A failed flush loses nothing: the
// next one carries what it did.
func (m *Meter) Flush(ctx context.Context, now time.Time) error {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	var todo []pending
	var rows []Row
	m.accounts.Range(func(_, v any) bool {
		a := v.(*account)
		a.mu.Lock()
		defer a.mu.Unlock()
		for day, ds := range a.days {
			r := Row{KeyID: a.id, Day: day,
				CostMicro:      ds.b.cost.Load() - ds.flushedCost,
				EstimatedMicro: ds.b.estimated.Load() - ds.flushedEst,
				Requests:       ds.b.requests.Load() - ds.flushedReq,
				Unmetered:      ds.b.unmetered.Load() - ds.flushedUnmetered,
			}
			if r != (Row{KeyID: a.id, Day: day}) {
				todo = append(todo, pending{ds, r})
				rows = append(rows, r)
			}
		}
		return true
	})
	// With nothing to add, a flush after a failure still asks the counters: a
	// refused request adds no spend, so nothing else would show the store is back.
	if len(rows) > 0 || m.failingSince.Load() != 0 {
		ctx, cancel := context.WithTimeout(ctx, flushTimeout)
		defer cancel()
		if err := m.opts.Counters.Add(ctx, rows); err != nil {
			m.failingSince.CompareAndSwap(0, now.UnixNano())
			return fmt.Errorf("meter: flush spend: %w", err)
		}
	}
	m.failingSince.Store(0)
	for _, p := range todo {
		p.state.flushedCost += p.row.CostMicro
		p.state.flushedEst += p.row.EstimatedMicro
		p.state.flushedReq += p.row.Requests
		p.state.flushedUnmetered += p.row.Unmetered
	}
	m.forgetOld(now)
	return nil
}

// forgetOld lets go of days and months that are over and fully flushed, so that
// a key's memory doesn't grow for as long as kman runs. It keeps this month's and
// last month's days, which a window may still point at.
func (m *Meter) forgetOld(now time.Time) {
	day, month := labels(now.AddDate(0, 0, -2), m.opts.Location)
	m.accounts.Range(func(_, v any) bool {
		a := v.(*account)
		a.mu.Lock()
		defer a.mu.Unlock()
		for label, ds := range a.days {
			b := ds.b
			if label < day && b.held.Load() == b.cost.Load() && ds.flushedCost == b.cost.Load() &&
				ds.flushedReq == b.requests.Load() && ds.flushedUnmetered == b.unmetered.Load() && ds.flushedEst == b.estimated.Load() {
				delete(a.days, label)
			}
		}
		for label := range a.months {
			if label < month {
				delete(a.months, label)
			}
		}
		return true
	})
}
