package events

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// Default queue sizes: an event is a few hundred bytes, so these hold a few
// MiB at most.
const (
	DefaultCapacity         = 8192
	DefaultPriorityCapacity = 512
)

// Options sizes a Bus.
type Options struct {
	// Capacity is how many ordinary events wait to be fanned out, and
	// PriorityCapacity how many priority events do. Past either, Emit drops.
	Capacity, PriorityCapacity int
}

// Stats is what a Bus has counted.
type Stats struct {
	Queued          int   // events waiting now, in both lanes
	Dropped         int64 // ordinary events dropped because the queue was full
	DroppedPriority int64 // priority events dropped because their lane was full
}

// Bus is a bounded queue with a priority lane, and the fan-out of what leaves
// it to subscribers. Emit never blocks: the request path may call it freely.
type Bus struct {
	normal   chan Event
	priority chan Event

	dropped     atomic.Int64
	droppedPrio atomic.Int64

	mu     sync.Mutex
	subs   []*Subscription
	closed bool
}

// New returns a Bus. Its events are fanned out once Run is going.
func New(o Options) *Bus {
	if o.Capacity <= 0 {
		o.Capacity = DefaultCapacity
	}
	if o.PriorityCapacity <= 0 {
		o.PriorityCapacity = DefaultPriorityCapacity
	}
	return &Bus{normal: make(chan Event, o.Capacity), priority: make(chan Event, o.PriorityCapacity)}
}

// Emit queues e. If the queue is full, e is dropped and counted (Stats); Emit
// never waits for room.
func (b *Bus) Emit(e Event) {
	lane, drops := b.normal, &b.dropped
	if e.Priority {
		lane, drops = b.priority, &b.droppedPrio
	}
	select {
	case lane <- e:
	default:
		drops.Add(1)
	}
}

// Stats returns what the Bus has counted so far.
func (b *Bus) Stats() Stats {
	return Stats{
		Queued:          len(b.normal) + len(b.priority),
		Dropped:         b.dropped.Load(),
		DroppedPriority: b.droppedPrio.Load(),
	}
}

// SubOptions shapes one subscription.
type SubOptions struct {
	// Buffer is how many events the subscription holds for a reader that is
	// behind. A full subscription drops (Subscription.Dropped): one slow reader
	// never holds up the others.
	Buffer int
	// PriorityOnly limits the subscription to events from the priority lane.
	PriorityOnly bool
}

// Subscription is one subscriber's share of the event stream.
type Subscription struct {
	Name string
	// C carries the events. It is closed once the Bus has stopped and everything
	// queued has been handed on.
	C <-chan Event

	c       chan Event
	prio    bool
	dropped atomic.Int64
}

// Dropped is how many events were not handed to this subscription because its
// buffer was full.
func (s *Subscription) Dropped() int64 { return s.dropped.Load() }

// Subscribe adds a subscriber. It sees the events dispatched from now on. After
// the Bus has stopped, the subscription it returns is already closed.
func (b *Bus) Subscribe(name string, o SubOptions) *Subscription {
	if o.Buffer <= 0 {
		o.Buffer = 256
	}
	c := make(chan Event, o.Buffer)
	s := &Subscription{Name: name, C: c, c: c, prio: o.PriorityOnly}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(c)
		return s
	}
	b.subs = append(b.subs, s)
	return s
}

// Run fans events out until ctx ends, taking the priority lane first. Then it
// hands on what was still queued and closes every subscription, so that a
// subscriber can finish what it holds. Call it once.
func (b *Bus) Run(ctx context.Context) {
	for {
		select {
		case e := <-b.priority:
			b.dispatch(e)
			continue
		default:
		}
		select {
		case e := <-b.priority:
			b.dispatch(e)
		case e := <-b.normal:
			b.dispatch(e)
		case <-ctx.Done():
			b.finish()
			return
		}
	}
}

// finish hands on what is queued, priority first, and closes the subscriptions.
func (b *Bus) finish() {
	for {
		select {
		case e := <-b.priority:
			b.dispatch(e)
			continue
		default:
		}
		select {
		case e := <-b.normal:
			b.dispatch(e)
			continue
		default:
		}
		break
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, s := range b.subs {
		close(s.c)
	}
	b.subs = nil
}

func (b *Bus) dispatch(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if s.prio && !e.Priority {
			continue
		}
		select {
		case s.c <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

// Subscriptions returns the current subscriptions, for reading their counts.
func (b *Bus) Subscriptions() []*Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.subs)
}
