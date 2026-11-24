package events_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/events"
)

// run starts b's dispatcher and returns a func that stops it and waits.
func run(t *testing.T, b *events.Bus) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

// drain reads every event a subscription has until it is closed.
func drain(s *events.Subscription) []events.Event {
	var out []events.Event
	for e := range s.C {
		out = append(out, e)
	}
	return out
}

func TestAnEmittedEventReachesEverySubscriber(t *testing.T) {
	b := events.New(events.Options{})
	one := b.Subscribe("one", events.SubOptions{Buffer: 8})
	two := b.Subscribe("two", events.SubOptions{Buffer: 8})
	stop := run(t, b)

	b.Emit(events.Event{Kind: events.KindRequest, KeyID: "k1", Status: 200})
	b.Emit(events.Event{Kind: events.KindBlocked, KeyID: "k2", BlockedCode: "key_expired"})
	stop() // drains what was queued, then closes the subscriptions

	for _, s := range []*events.Subscription{one, two} {
		got := drain(s)
		if len(got) != 2 || got[0].KeyID != "k1" || got[1].BlockedCode != "key_expired" {
			t.Errorf("subscriber %s got %+v, want both events in order", s.Name, got)
		}
	}
}

func TestEventsEmittedBeforeTheBusRunsAreKept(t *testing.T) {
	b := events.New(events.Options{})
	sub := b.Subscribe("s", events.SubOptions{Buffer: 8})
	b.Emit(events.Event{KeyID: "early"})
	stop := run(t, b)
	stop()
	if got := drain(sub); len(got) != 1 || got[0].KeyID != "early" {
		t.Fatalf("got %+v, want the early event", got)
	}
}

func TestAFullQueueNeverBlocksEmitAndTheDropsAreCounted(t *testing.T) {
	// No dispatcher is running, so nothing leaves the queue.
	b := events.New(events.Options{Capacity: 4, PriorityCapacity: 2})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			b.Emit(events.Event{KeyID: "k"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked on a full queue")
	}
	st := b.Stats()
	if st.Queued != 4 || st.Dropped != 96 {
		t.Fatalf("stats %+v, want 4 queued and 96 dropped", st)
	}
}

func TestAFullPriorityLaneNeverBlocksEitherAndItsDropsAreCounted(t *testing.T) {
	b := events.New(events.Options{Capacity: 4, PriorityCapacity: 2})
	for range 5 {
		b.Emit(events.Event{KeyID: "k", Priority: true})
	}
	st := b.Stats()
	if st.DroppedPriority != 3 || st.Dropped != 0 {
		t.Fatalf("stats %+v, want 3 priority drops and no ordinary drops", st)
	}
}

func TestPriorityEventsAreDeliveredBeforeQueuedOrdinaryOnes(t *testing.T) {
	b := events.New(events.Options{Capacity: 16, PriorityCapacity: 16})
	sub := b.Subscribe("s", events.SubOptions{Buffer: 16})
	for range 5 {
		b.Emit(events.Event{KeyID: "ordinary"})
	}
	b.Emit(events.Event{KeyID: "disabled", Priority: true})
	stop := run(t, b)
	stop()
	got := drain(sub)
	if len(got) != 6 || got[0].KeyID != "disabled" {
		t.Fatalf("got %+v, want the priority event first of six", got)
	}
}

func TestAPriorityOnlySubscriberSeesOnlyPriorityEvents(t *testing.T) {
	b := events.New(events.Options{})
	all := b.Subscribe("all", events.SubOptions{Buffer: 8})
	prio := b.Subscribe("alerts", events.SubOptions{Buffer: 8, PriorityOnly: true})
	b.Emit(events.Event{KeyID: "a"})
	b.Emit(events.Event{KeyID: "b", Priority: true})
	stop := run(t, b)
	stop()
	if got := drain(prio); len(got) != 1 || got[0].KeyID != "b" {
		t.Errorf("priority-only subscriber got %+v, want only b", got)
	}
	if got := drain(all); len(got) != 2 {
		t.Errorf("full subscriber got %d events, want 2", len(got))
	}
}

func TestASlowSubscriberDoesNotHoldUpTheOthersAndItsDropsAreCounted(t *testing.T) {
	b := events.New(events.Options{Capacity: 64})
	slow := b.Subscribe("slow", events.SubOptions{Buffer: 2}) // never read until the end
	fast := b.Subscribe("fast", events.SubOptions{Buffer: 64})
	for range 10 {
		b.Emit(events.Event{KeyID: "k"})
	}
	stop := run(t, b)
	stop()
	if got := drain(fast); len(got) != 10 {
		t.Errorf("fast subscriber got %d events, want 10", len(got))
	}
	if got := drain(slow); len(got) != 2 {
		t.Errorf("slow subscriber got %d events, want its buffer's 2", len(got))
	}
	if slow.Dropped() != 8 || fast.Dropped() != 0 {
		t.Errorf("drops: slow %d, fast %d; want 8 and 0", slow.Dropped(), fast.Dropped())
	}
}

func TestSubscribingAfterTheBusStoppedGivesAClosedSubscription(t *testing.T) {
	b := events.New(events.Options{})
	stop := run(t, b)
	stop()
	sub := b.Subscribe("late", events.SubOptions{Buffer: 1})
	if _, open := <-sub.C; open {
		t.Fatal("a subscription made after the bus stopped should be closed")
	}
}

func TestEmitAfterTheBusStoppedDoesNotPanicOrBlock(t *testing.T) {
	b := events.New(events.Options{Capacity: 2})
	stop := run(t, b)
	stop()
	for range 10 {
		b.Emit(events.Event{KeyID: "k"})
	}
	if b.Stats().Dropped == 0 {
		t.Fatal("events emitted to a stopped bus past its capacity should count as dropped")
	}
}

func TestConcurrentEmittersAreSafe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := events.New(events.Options{Capacity: 1000})
		sub := b.Subscribe("s", events.SubOptions{Buffer: 1000})
		stop := run(t, b)
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				for range 50 {
					b.Emit(events.Event{KeyID: "k"})
				}
			})
		}
		wg.Wait()
		stop()
		if got := drain(sub); len(got) != 500 {
			t.Fatalf("got %d events, want 500", len(got))
		}
	})
}

func TestUnsubscribeStopsDeliveryAndClosesTheChannel(t *testing.T) {
	b := events.New(events.Options{})
	gone := b.Subscribe("gone", events.SubOptions{Buffer: 8})
	kept := b.Subscribe("kept", events.SubOptions{Buffer: 8})
	stop := run(t, b)

	b.Emit(events.Event{KeyID: "before"})
	if e := <-gone.C; e.KeyID != "before" {
		t.Fatalf("got %+v before unsubscribing", e)
	}
	b.Unsubscribe(gone)
	b.Unsubscribe(gone) // a second time is harmless
	b.Emit(events.Event{KeyID: "after"})
	stop()

	if got := drain(gone); len(got) != 0 {
		t.Errorf("an unsubscribed subscription got %+v", got)
	}
	if got := drain(kept); len(got) != 2 {
		t.Errorf("the other subscription got %d events, want 2", len(got))
	}
	if n := len(b.Subscriptions()); n != 0 {
		t.Errorf("%d subscriptions remain after the bus stopped", n)
	}
}

func TestUnsubscribeReleasesTheSubscriptionAndAfterTheBusStoppedIsHarmless(t *testing.T) {
	b := events.New(events.Options{})
	sub := b.Subscribe("s", events.SubOptions{Buffer: 1})
	if n := len(b.Subscriptions()); n != 1 {
		t.Fatalf("%d subscriptions, want 1", n)
	}
	b.Unsubscribe(sub)
	if n := len(b.Subscriptions()); n != 0 {
		t.Fatalf("%d subscriptions after Unsubscribe, want none", n)
	}
	other := b.Subscribe("other", events.SubOptions{Buffer: 1})
	stop := run(t, b)
	stop()
	b.Unsubscribe(other) // the bus closed it already
	b.Unsubscribe(sub)
}

func TestWhatAnUnsubscribedSubscriptionDroppedIsStillCountedByName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := events.New(events.Options{Capacity: 16})
		one := b.Subscribe("event-stream", events.SubOptions{Buffer: 1})
		two := b.Subscribe("event-stream", events.SubOptions{Buffer: 1})
		stop := run(t, b)
		for range 3 {
			b.Emit(events.Event{KeyID: "k"})
		}
		synctest.Wait() // the bus has handed all three on
		if one.Dropped() != 2 || two.Dropped() != 2 {
			t.Fatalf("drops %d and %d, want 2 each", one.Dropped(), two.Dropped())
		}
		b.Unsubscribe(one)
		if got := b.SubscriberDrops()["event-stream"]; got != 4 {
			t.Errorf("drops by name after one left = %d, want 4 (2 released and 2 live)", got)
		}
		stop()
	})
}
