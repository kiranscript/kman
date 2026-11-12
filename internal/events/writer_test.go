package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/events"
)

// sink records the batches it is given, and fails while failing is set.
type sink struct {
	mu      sync.Mutex
	batches [][]events.Event
	failing bool
}

func (s *sink) AddEvents(_ context.Context, batch []events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing {
		return errors.New("the store is locked")
	}
	s.batches = append(s.batches, append([]events.Event(nil), batch...))
	return nil
}

func (s *sink) setFailing(v bool) { s.mu.Lock(); s.failing = v; s.mu.Unlock() }

func (s *sink) sizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for _, b := range s.batches {
		out = append(out, len(b))
	}
	return out
}

// startWriter runs a bus with a writer subscribed. stop ends the bus, which
// closes the writer's subscription, and waits for the writer to finish.
func startWriter(t *testing.T, o events.WriterOptions) (b *events.Bus, w *events.Writer, stop func()) {
	t.Helper()
	b = events.New(events.Options{})
	w = events.NewWriter(o)
	sub := b.Subscribe("store", events.SubOptions{Buffer: 1000})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { b.Run(ctx) })
	wg.Go(func() { w.Run(sub) })
	return b, w, func() { cancel(); wg.Wait() }
}

func TestTheWriterBatchesEventsUntilTheFlushInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &sink{}
		b, _, stop := startWriter(t, events.WriterOptions{Sink: s, FlushEvery: 5 * time.Second, BatchSize: 100})
		for range 3 {
			b.Emit(events.Event{KeyID: "k"})
		}
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if n := len(s.sizes()); n != 0 {
			t.Fatalf("%d batches written before the flush interval", n)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := s.sizes(); len(got) != 1 || got[0] != 3 {
			t.Fatalf("batches %v, want one of 3", got)
		}
		stop()
	})
}

func TestTheWriterFlushesAtOnceWhenABatchIsFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &sink{}
		b, _, stop := startWriter(t, events.WriterOptions{Sink: s, FlushEvery: time.Hour, BatchSize: 4})
		for range 9 {
			b.Emit(events.Event{KeyID: "k"})
		}
		synctest.Wait()
		if got := s.sizes(); len(got) != 2 || got[0] != 4 || got[1] != 4 {
			t.Fatalf("batches %v, want two of 4 with 1 left over", got)
		}
		stop()
		if got := s.sizes(); len(got) != 3 || got[2] != 1 {
			t.Fatalf("batches %v after stopping, want the last one flushed", got)
		}
	})
}

func TestTheWriterWritesWhatIsLeftWhenTheBusStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &sink{}
		b, w, stop := startWriter(t, events.WriterOptions{Sink: s, FlushEvery: time.Hour})
		b.Emit(events.Event{KeyID: "a"})
		b.Emit(events.Event{KeyID: "b"})
		stop()
		if got := s.sizes(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("batches %v, want the two events in one", got)
		}
		if st := w.Stats(); st.Written != 2 || st.Lost != 0 {
			t.Fatalf("stats %+v, want 2 written", st)
		}
	})
}

func TestAFailedWriteIsRetriedWithNothingLostOrRepeated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &sink{failing: true}
		b, w, stop := startWriter(t, events.WriterOptions{Sink: s, FlushEvery: time.Second})
		b.Emit(events.Event{KeyID: "a"})
		time.Sleep(2500 * time.Millisecond)
		synctest.Wait()
		if st := w.Stats(); st.Failures < 2 || st.Written != 0 {
			t.Fatalf("stats %+v, want repeated failures and nothing written", st)
		}
		b.Emit(events.Event{KeyID: "b"})
		synctest.Wait()
		s.setFailing(false)
		time.Sleep(time.Second)
		synctest.Wait()
		stop()
		if got := s.sizes(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("batches %v, want both events once", got)
		}
		if st := w.Stats(); st.Written != 2 || st.Lost != 0 {
			t.Fatalf("stats %+v, want 2 written and none lost", st)
		}
	})
}

func TestAWriterThatCannotWriteKeepsABoundedBacklogAndCountsWhatItLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &sink{failing: true}
		b, w, stop := startWriter(t, events.WriterOptions{Sink: s, FlushEvery: time.Second, BatchSize: 1000, MaxPending: 3})
		for i := range 5 {
			b.Emit(events.Event{KeyID: "k", ReqBytes: int64(i)})
		}
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		s.setFailing(false)
		time.Sleep(time.Second)
		synctest.Wait()
		stop()
		if st := w.Stats(); st.Lost != 2 || st.Written != 3 {
			t.Fatalf("stats %+v, want the 2 oldest lost and 3 written", st)
		}
		got := s.batches[0]
		if len(got) != 3 || got[0].ReqBytes != 2 {
			t.Fatalf("written %+v, want the newest three (2, 3, 4)", got)
		}
	})
}
