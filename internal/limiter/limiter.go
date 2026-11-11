// Package limiter enforces per-key RPM limits and throttles invalid keys by
// address, in bounded LRU memory.
package limiter

import (
	"container/list"
	"math"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults of Options.
const (
	DefaultMaxKeys          = 10_000
	DefaultMaxAddrs         = 10_000
	DefaultInvalidPerMinute = 30
)

// window is how long a limit's count looks back.
const window = time.Minute

// Options are the limiter's bounds and clock.
type Options struct {
	Now func() time.Time // required
	// MaxKeys and MaxAddrs bound how many keys and addresses are remembered. Past
	// them the least recently used is forgotten.
	MaxKeys, MaxAddrs int
	// InvalidPerMinute is how many invalid keys an address may present in a
	// minute before it is throttled.
	InvalidPerMinute int
}

// Limiter holds RPM windows for keys and invalid-key counts for addresses.
// Its memory is bounded by Options, however many keys and addresses a flood
// makes up: a table that grew with them would be a way to exhaust the server.
type Limiter struct {
	now     func() time.Time
	invalid int
	keys    table[string]
	addrs   table[netip.Addr]
}

// New returns a Limiter.
func New(o Options) *Limiter {
	if o.Now == nil {
		panic("limiter: Options.Now is required: only main reads the clock")
	}
	if o.MaxKeys <= 0 {
		o.MaxKeys = DefaultMaxKeys
	}
	if o.MaxAddrs <= 0 {
		o.MaxAddrs = DefaultMaxAddrs
	}
	if o.InvalidPerMinute <= 0 {
		o.InvalidPerMinute = DefaultInvalidPerMinute
	}
	l := &Limiter{now: o.Now, invalid: o.InvalidPerMinute}
	l.keys.init(o.MaxKeys)
	l.addrs.init(o.MaxAddrs)
	return l
}

// Allow counts a request of the key against its limit of rpm requests a
// minute, which a caller reads from the key's policy on every request so an edit
// takes effect at once. It reports false when the key is over, with how long
// until it is likely to be under again. A refused request is not counted. An rpm
// of 0 is no limit, and tracks nothing.
func (l *Limiter) Allow(key string, rpm int) (ok bool, retryAfter time.Duration) {
	if rpm <= 0 {
		return true, 0
	}
	now := l.now()
	l.keys.mu.Lock()
	defer l.keys.mu.Unlock()
	w := l.keys.get(key, now)
	return w.take(now, float64(rpm))
}

// AllowInvalid reports whether an address may present keys: false once it has
// presented too many invalid ones in the last minute. It is asked before a key
// is even parsed, and changes nothing. A throttled address is refused whether or
// not its key is good, so guessing can't go on at the speed of the server.
func (l *Limiter) AllowInvalid(addr netip.Addr) bool {
	if l.addrs.size.Load() == 0 {
		return true // no address has presented an invalid key: the usual case takes no lock
	}
	addr = addrKey(addr)
	l.addrs.mu.RLock() // every request asks, so asking shares the lock
	defer l.addrs.mu.RUnlock()
	w, ok := l.addrs.peek(addr)
	return !ok || w.estimate(l.now()) < float64(l.invalid)
}

// RecordInvalid notes that an address presented an invalid key.
func (l *Limiter) RecordInvalid(addr netip.Addr) {
	addr = addrKey(addr)
	now := l.now()
	l.addrs.mu.Lock()
	defer l.addrs.mu.Unlock()
	l.addrs.get(addr, now).add(now)
}

// Tracked is how many keys and addresses are remembered now.
func (l *Limiter) Tracked() (keys, addrs int) {
	l.keys.mu.Lock()
	keys = l.keys.order.Len()
	l.keys.mu.Unlock()
	l.addrs.mu.Lock()
	addrs = l.addrs.order.Len()
	l.addrs.mu.Unlock()
	return keys, addrs
}

// addrKey is what an address is counted as: an IPv6 network of 64 bits is one
// subscriber's, who holds 2^64 addresses, so counting each would make the
// throttle free to get round, and the table free to flood.
func addrKey(a netip.Addr) netip.Addr {
	a = a.Unmap().WithZone("")
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.Addr()
	}
	return a
}

// sliding is a sliding-window counter: the requests of the current window and
// of the one before it, the earlier weighed by how much of it is still within
// the last minute. It needs two numbers however many requests there are.
type sliding struct {
	start     time.Time // when the current window began
	cur, prev float64
}

func (w *sliding) roll(now time.Time) {
	elapsed := now.Sub(w.start)
	switch {
	case elapsed < 0: // the clock stepped back
		w.start, w.cur, w.prev = now, 0, 0
	case elapsed >= 2*window:
		w.start, w.cur, w.prev = now, 0, 0
	case elapsed >= window:
		w.start, w.prev, w.cur = w.start.Add(window), w.cur, 0
	}
}

func (w *sliding) estimate(now time.Time) float64 {
	c := *w
	c.roll(now)
	into := float64(now.Sub(c.start)) / float64(window)
	return c.prev*(1-math.Min(math.Max(into, 0), 1)) + c.cur
}

func (w *sliding) add(now time.Time) {
	w.roll(now)
	w.cur++
}

func (w *sliding) take(now time.Time, limit float64) (bool, time.Duration) {
	if w.estimate(now)+1 > limit {
		// By the end of this window the earlier one weighs nothing, and the current
		// one is the whole of it: a bound, not a promise.
		w.roll(now)
		retry := w.start.Add(window).Sub(now)
		return false, max(retry, time.Second)
	}
	w.add(now)
	return true, 0
}

// table is an LRU map of windows, which the owner locks.
type table[K comparable] struct {
	mu    sync.RWMutex
	max   int
	order *list.List // front = most recently used; holds *entry[K]
	index map[K]*list.Element
	size  atomic.Int64 // len(index), readable without the lock
}

type entry[K comparable] struct {
	key K
	w   sliding
}

func (t *table[K]) init(max int) {
	t.max = max
	t.order = list.New()
	t.index = make(map[K]*list.Element)
}

// get returns the key's window, making it, and the least recently used one's
// room, if it is new.
func (t *table[K]) get(k K, now time.Time) *sliding {
	if el, ok := t.index[k]; ok {
		t.order.MoveToFront(el)
		return &el.Value.(*entry[K]).w
	}
	if t.order.Len() >= t.max {
		oldest := t.order.Back()
		delete(t.index, oldest.Value.(*entry[K]).key)
		t.order.Remove(oldest)
	}
	e := &entry[K]{key: k, w: sliding{start: now}}
	t.index[k] = t.order.PushFront(e)
	t.size.Store(int64(len(t.index)))
	return &e.w
}

// peek returns the key's window without making or touching one.
func (t *table[K]) peek(k K) (*sliding, bool) {
	el, ok := t.index[k]
	if !ok {
		return nil, false
	}
	return &el.Value.(*entry[K]).w, true
}
