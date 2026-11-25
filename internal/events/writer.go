package events

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// Writer defaults. SPEC §14 sets the flush interval at 5 s.
const (
	DefaultBatchSize  = 500
	DefaultFlushEvery = 5 * time.Second
	DefaultMaxPending = 10_000
	// writeTimeout bounds one write, so that a store that has hung can't keep
	// shutdown waiting.
	writeTimeout = 10 * time.Second
)

// Sink is where a Writer puts its batches: the store, which keeps the events and
// adds them to usage_hourly in one transaction.
type Sink interface {
	AddEvents(ctx context.Context, batch []Event) error
}

// Observer is told about each batch an SQLite sink stores, inside the
// transaction that stores it, so that what it writes commits with the batch or
// not at all. detect is one: a place a key is used from for the first time is
// new exactly once, and the detection it raises is in the same transaction as the
// place, so a crash cannot record the place and lose the detection (#89).
//
// An observer runs on the writer's goroutine, never on the request path, and
// adds to the transaction's time: it must be quick, and an error it returns
// fails the batch, which the writer retries.
type Observer interface {
	// ObservePlaces is given the batch's rows and the places the batch gave their
	// keys for the first time (store.NewPlace.Event indexes rows).
	ObservePlaces(tx *store.Tx, rows []store.EventRow, fresh []store.NewPlace) error
	// AfterCommit is called once a transaction in which ObservePlaces returned
	// without an error has committed, for what needs doing then, such as waking a
	// dispatcher. It is also called for a transaction that rolled back, which is
	// harmless to do too early.
	AfterCommit()
}

// SQLite returns a Sink that writes to the store's events and usage_hourly
// tables and key places, and tells each observer of every batch in the same
// transaction.
func SQLite(s *store.Store, observers ...Observer) Sink { return sqliteSink{s, observers} }

type sqliteSink struct {
	s         *store.Store
	observers []Observer
}

func (k sqliteSink) AddEvents(ctx context.Context, batch []Event) error {
	rows := make([]store.EventRow, len(batch))
	for i, e := range batch {
		var ip string
		if e.ClientIP.IsValid() {
			ip = e.ClientIP.String()
		}
		rows[i] = store.EventRow{
			Time: e.Time, Kind: string(e.Kind), KeyID: e.KeyID, ClientIP: ip, Country: e.Country, ASN: e.ASN, ASOrg: e.ASOrg, Client: e.Client,
			UpstreamID: e.UpstreamID, Model: e.Model, Operation: e.Operation(),
			Status: e.Status, BlockedCode: e.BlockedCode, LatencyMS: e.Latency.Milliseconds(),
			ReqBytes: e.ReqBytes, RespBytes: e.RespBytes,
			TokensIn: e.TokensIn, TokensOut: e.TokensOut, TokensCached: e.TokensCached, TokensCacheWrite: e.TokensCacheWrite,
			TokensAudioIn: e.TokensAudioIn, TokensAudioOut: e.TokensAudioOut,
			CostMicro: e.CostMicro, CostKnown: e.CostKnown, CostEstimated: e.CostEstimated,
		}
	}
	err := k.s.Do(ctx, func(tx *store.Tx) error {
		fresh, err := tx.AddEventsFresh(rows)
		if err != nil {
			return err
		}
		for _, o := range k.observers {
			if err := o.ObservePlaces(tx, rows, fresh); err != nil {
				return err
			}
		}
		return nil
	})
	for _, o := range k.observers {
		o.AfterCommit()
	}
	return err
}

// WriterOptions configures a Writer.
type WriterOptions struct {
	Sink Sink
	// BatchSize is how many events make a batch that is written at once, and
	// FlushEvery how long events wait for company before they are written anyway.
	BatchSize  int
	FlushEvery time.Duration
	// MaxPending bounds the events kept while the store can't be written to. Past
	// it the oldest are given up, and counted in Stats.
	MaxPending int
	Log        *slog.Logger // defaults to discarding
}

// WriterStats is what a Writer has counted.
type WriterStats struct {
	Written  int64 // events stored
	Failures int64 // writes that failed, each retried with the next batch
	Lost     int64 // events given up: the backlog was full, or the last write failed
}

// Writer takes events off a subscription and writes them to a Sink in batches.
type Writer struct {
	opts                    WriterOptions
	written, failures, lost atomic.Int64
}

// NewWriter returns a Writer.
func NewWriter(o WriterOptions) *Writer {
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = DefaultFlushEvery
	}
	if o.MaxPending <= 0 {
		o.MaxPending = DefaultMaxPending
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Writer{opts: o}
}

// Stats returns what the Writer has counted so far.
func (w *Writer) Stats() WriterStats {
	return WriterStats{Written: w.written.Load(), Failures: w.failures.Load(), Lost: w.lost.Load()}
}

// Run writes what sub delivers until it is closed, which is when the bus has
// stopped. Then it writes what is left, once more if need be, and returns.
func (w *Writer) Run(sub *Subscription) {
	var pending []Event
	t := time.NewTicker(w.opts.FlushEvery)
	defer t.Stop()
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				if !w.flush(&pending) && len(pending) > 0 {
					w.lost.Add(int64(len(pending)))
					w.opts.Log.Error("events could not be saved and are lost", "count", len(pending))
				}
				return
			}
			pending = append(pending, e)
			if over := len(pending) - w.opts.MaxPending; over > 0 {
				pending = append(pending[:0], pending[over:]...)
				w.lost.Add(int64(over))
			}
			if len(pending) >= w.opts.BatchSize {
				w.flush(&pending)
			}
		case <-t.C:
			w.flush(&pending)
		}
	}
}

// flush writes pending, and keeps it for the next try if that fails. It reports
// whether there is nothing left to write.
func (w *Writer) flush(pending *[]Event) bool {
	if len(*pending) == 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := w.opts.Sink.AddEvents(ctx, *pending); err != nil {
		w.failures.Add(1)
		w.opts.Log.Error("events could not be saved; they will be retried", "count", len(*pending), "err", err)
		return false
	}
	w.written.Add(int64(len(*pending)))
	*pending = (*pending)[:0]
	return true
}
