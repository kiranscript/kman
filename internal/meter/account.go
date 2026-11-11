package meter

import (
	"sync"
	"sync/atomic"
	"time"
)

// bucket is a key's spend in one day or one month. Everything in it is changed
// atomically: a request takes no lock.
type bucket struct {
	label string
	// held is what the key has spent plus what is reserved: what a cap is checked
	// against.
	held                                 atomic.Int64
	cost, estimated, requests, unmetered atomic.Int64
}

func (b *bucket) period() Period {
	cost := b.cost.Load()
	return Period{Label: b.label, CostMicro: cost, EstimatedMicro: b.estimated.Load(),
		ReservedMicro: max(b.held.Load()-cost, 0), Requests: b.requests.Load(), Unmetered: b.unmetered.Load()}
}

// admit holds n more in b, unless that would pass the cap, which is zero for
// none. It is a compare-and-swap loop, so two requests can't both be admitted by
// the room for one.
func (b *bucket) admit(n, limit int64) bool {
	for {
		cur := b.held.Load()
		if limit > 0 && (cur > limit || n > limit-cur) {
			return false
		}
		if b.held.CompareAndSwap(cur, cur+min(n, 1<<62-cur)) { // saturating: an estimate can be MaxInt64
			return true
		}
	}
}

// window is the day and month a moment falls in, which hold for every moment from
// start up to end.
type window struct {
	start, end time.Time
	day, month *bucket
}

// admit holds n against the day and the month, within their caps, or neither.
func (w *window) admit(n, dailyCap, monthlyCap int64) bool {
	if !w.day.admit(n, dailyCap) {
		return false
	}
	if !w.month.admit(n, monthlyCap) {
		w.day.held.Add(-n)
		return false
	}
	return true
}

func (w *window) release(n int64) {
	w.day.held.Add(-n)
	w.month.held.Add(-n)
}

func (w *window) countUnmetered() {
	w.day.unmetered.Add(1)
	w.month.unmetered.Add(1)
}

// charge replaces what a request reserved in the window it was taken in
// (taken) with its cost in this one. Across midnight these differ.
func (w *window) charge(taken *window, reserved int64, c Charge, counted bool) {
	taken.release(reserved)
	for _, b := range [2]*bucket{w.day, w.month} {
		b.held.Add(c.Micro)
		b.cost.Add(c.Micro)
		if c.Estimated {
			b.estimated.Add(c.Micro)
		}
		if counted {
			b.requests.Add(1)
		}
	}
}

// dayState is a day's bucket and how much of it has reached the counters.
type dayState struct {
	b                                                     *bucket
	flushedCost, flushedEst, flushedReq, flushedUnmetered int64 // only a flush touches these
}

// account is one key's spend.
type account struct {
	id  string
	win atomic.Pointer[window]

	mu     sync.Mutex // for rolling over to a new day, and for a flush: not for a request in the day
	days   map[string]*dayState
	months map[string]*bucket
}

// labels are the day's and the month's label at t in loc.
func labels(t time.Time, loc *time.Location) (day, month string) {
	day = t.In(loc).Format(time.DateOnly)
	return day, day[:len("2006-01")]
}

// window returns the window holding now. It rolls over to a new one when the day
// has changed, which takes the lock once a day for each key.
func (a *account) window(now time.Time, loc *time.Location) *window {
	if w := a.win.Load(); w != nil && !now.Before(w.start) && now.Before(w.end) {
		return w
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	local := now.In(loc)
	y, mo, d := local.Date()
	start := time.Date(y, mo, d, 0, 0, 0, 0, loc)
	end := time.Date(y, mo, d+1, 0, 0, 0, 0, loc)
	dayLabel, monthLabel := labels(now, loc)
	if w := a.win.Load(); w != nil && w.day.label == dayLabel {
		// The same day found again, as when DST makes the day's bounds differ.
		return w
	}
	ds := a.days[dayLabel]
	if ds == nil {
		ds = &dayState{b: &bucket{label: dayLabel}}
		a.days[dayLabel] = ds
	}
	month := a.months[monthLabel]
	if month == nil {
		month = &bucket{label: monthLabel}
		a.months[monthLabel] = month
	}
	w := &window{start: start, end: end, day: ds.b, month: month}
	a.win.Store(w)
	return w
}
