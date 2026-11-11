package meter

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/profiles"
)

// Why a request is refused. A refusal reserves nothing.
var (
	// ErrCapReached: committed spend, open reservations and this request's
	// reservation would together pass a cap.
	ErrCapReached = errors.New("meter: spend cap reached")
	// ErrUnmetered: the request can't be metered, and its key may send only
	// requests that can (a cap, a model allowlist).
	ErrUnmetered = errors.New("meter: request can't be metered")
	// ErrUnknownPrice: the request's model has no price, on a key that may send
	// only requests that can be metered. It is always an *UnknownPriceError.
	ErrUnknownPrice = errors.New("meter: no price for the model")
	// ErrStoreUnavailable: spend hasn't reached the store for too long, so a
	// capped key can't be sure of what it has spent.
	ErrStoreUnavailable = errors.New("meter: spend can't be recorded")
)

// UnknownPriceError says which model had no price.
type UnknownPriceError struct{ Model string }

func (e *UnknownPriceError) Error() string {
	return fmt.Sprintf("meter: no price for model %q", e.Model)
}

func (e *UnknownPriceError) Is(target error) bool { return target == ErrUnknownPrice }

// Defaults of Options.
const (
	// FlushEvery is how often spend reaches the counters (SPEC §8).
	FlushEvery = 5 * time.Second
	// FailClosedAfter is how long flushes may fail before a capped key is refused.
	FailClosedAfter = 60 * time.Second
)

// Options configures a Meter.
type Options struct {
	// Prices is the table in force. Every request reads it once.
	Prices *pricing.Prices
	// Counters is where spend is kept between runs.
	Counters Counters
	// Location is the spend_timezone: days and months begin at 00:00 in it. UTC when nil.
	Location *time.Location
	// Now is the clock Run's flushes use. Reserve and Settle take their time as an argument.
	Now func() time.Time
	// FlushEvery and FailClosedAfter default to the constants of those names.
	FlushEvery, FailClosedAfter time.Duration
	Log                         *slog.Logger
}

// Meter keeps every key's spend.
type Meter struct {
	opts     Options
	accounts sync.Map // key id → *account
	flushMu  sync.Mutex
	// failingSince is when the flushes began to fail, in Unix nanoseconds, or zero
	// while they succeed.
	failingSince atomic.Int64
}

// New returns a Meter with nothing spent. Load brings in what an earlier run
// spent this month.
func New(opts Options) *Meter {
	if opts.Location == nil {
		opts.Location = time.UTC
	}
	if opts.FlushEvery <= 0 {
		opts.FlushEvery = FlushEvery
	}
	if opts.FailClosedAfter <= 0 {
		opts.FailClosedAfter = FailClosedAfter
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Meter{opts: opts}
}

// Ask is a request, as far as the meter needs to know it.
type Ask struct {
	KeyID string
	// UpstreamID, Preset and MultiplierPPM are the upstream's, as the price table
	// wants them: it finds an upstream's overrides by id and its built-in prices by
	// preset, never by name, and the multiplier is its regional uplift.
	UpstreamID, Preset string
	MultiplierPPM      int64

	// Meters says the upstream is metered: every request has spend. Metered says the
	// request names a model, which a request that can be priced does.
	Meters, Metered bool
	Model           string
	MaxTokens       int64
	ServiceTier     string
	RequestBytes    int64

	// DailyCapMicro and MonthlyCapMicro are the key's caps, zero for none. Strict
	// says the key may send only requests that can be metered (a cap or a model
	// allowlist), so any other is refused.
	DailyCapMicro, MonthlyCapMicro int64
	Strict                         bool
}

// capped reports whether the key has a cap, which is what makes it fail closed.
func (a *Ask) capped() bool { return a.DailyCapMicro > 0 || a.MonthlyCapMicro > 0 }

// Reservation is a claim on a key's spend, held from before a request is
// forwarded until it is settled or released.
type Reservation struct {
	acct   *account
	win    *window // the day and month it was taken in
	table  *pricing.Table
	amount int64 // what is held, which is zero for a request with no price
	priced bool
	done   atomic.Bool

	upstreamID, preset string
	multiplier         int64
	model, tier        string
}

// Reserve takes a reservation for a request, or refuses it. It returns a nil
// Reservation, and no error, when there is nothing to reserve: the upstream
// isn't metered, or the request can't be and its key may send one that can't.
// Settle and Release take a nil one.
func (m *Meter) Reserve(now time.Time, a Ask) (*Reservation, error) {
	if !a.Meters {
		if a.Strict {
			return nil, ErrUnmetered
		}
		return nil, nil
	}
	if a.capped() && m.failedClosed(now) {
		return nil, ErrStoreUnavailable
	}
	acct := m.account(a.KeyID)
	win := acct.window(now, m.opts.Location)
	table := m.opts.Prices.Table()

	if !a.Metered {
		if a.Strict {
			return nil, ErrUnmetered
		}
		win.countUnmetered()
		return nil, nil
	}
	est, priced := table.Estimate(pricing.Request{
		UpstreamID: a.UpstreamID, Preset: a.Preset, Model: a.Model, MultiplierPPM: a.MultiplierPPM,
		RequestBytes: a.RequestBytes, MaxTokens: a.MaxTokens, ServiceTier: a.ServiceTier,
	})
	r := &Reservation{acct: acct, win: win, table: table, priced: priced,
		upstreamID: a.UpstreamID, preset: a.Preset, multiplier: a.MultiplierPPM, model: a.Model, tier: a.ServiceTier}
	if !priced {
		if a.Strict {
			return nil, &UnknownPriceError{Model: a.Model}
		}
		return r, nil // forwarded, and counted as unmetered unless the response reports its cost
	}
	if !win.admit(est.Micro, a.DailyCapMicro, a.MonthlyCapMicro) {
		return nil, ErrCapReached
	}
	r.amount = est.Micro
	return r, nil
}

// Outcome is how a request ended.
type Outcome struct {
	// Status is the upstream's, or zero when it never answered.
	Status int
	// Usage is what the response reported. A broken stream's is what it had
	// reported by then.
	Usage profiles.Usage
	// Broken says the response couldn't be finished: the upstream or the app broke
	// off, so what was used can't be known.
	Broken bool
	// Free says the route costs nothing and reports no usage.
	Free bool
}

// Charge is what a request was charged.
type Charge struct {
	Micro int64
	// Estimated says it is the reservation, charged because the real cost is unknown.
	Estimated bool
	Model     string // the model charged, when it was priced by one
	// Unknown says the cost can't be known: the model has no price and the
	// response reported none. Micro is then zero, which is not a cost of zero.
	Unknown bool
}

// Settle replaces a reservation with what the request cost, and says what that
// was. It takes effect once: a reservation already settled or released is left as
// it is. A nil reservation is a request nothing was reserved for.
//
//   - A response that reported usage is charged it, whatever its status: at the
//     model, the service tier and the cost it reported.
//   - A broken stream, or an app that left, is charged the whole reservation as an
//     estimate: how much ran is unknown.
//   - Any other 2xx that reported nothing is charged the reservation as an
//     estimate too, unless its route is free. It ran, and may have cost anything,
//     so letting it through free would put a cap one route away.
//   - A response that isn't 2xx and reported nothing costs nothing.
func (m *Meter) Settle(now time.Time, r *Reservation, o Outcome) Charge {
	if r == nil || !r.done.CompareAndSwap(false, true) {
		return Charge{}
	}
	var c Charge
	counted := o.Status != 0 || o.Broken
	switch {
	case !r.priced:
		// The model has no price, so only a cost the provider reported can be charged.
		if o.Usage.ReportedCost && !o.Broken {
			c = Charge{Micro: max(o.Usage.ReportedMicro, 0)}
		} else {
			counted = false
			c.Unknown = true
			r.win.countUnmetered()
		}
	case o.Broken:
		c = Charge{Micro: r.amount, Estimated: true}
	case o.Usage.Seen || o.Usage.ReportedCost:
		c = r.cost(o.Usage)
	case o.Free:
	case o.Status >= 200 && o.Status < 300:
		c = Charge{Micro: r.amount, Estimated: true}
	}
	r.acct.window(now, m.opts.Location).charge(r.win, r.amount, c, counted)
	return c
}

// cost prices what a response reported, or charges the reservation as an
// estimate when the table has gone without a price the reservation had.
func (r *Reservation) cost(u profiles.Usage) Charge {
	priced, ok := r.table.Cost(pricing.Usage{
		UpstreamID: r.upstreamID, Preset: r.preset, MultiplierPPM: r.multiplier,
		Model: cmpOr(u.Model, r.model), RequestedModel: r.model, ServiceTier: cmpOr(u.ServiceTier, r.tier),
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens,
		CacheWriteTokens: u.CacheWriteTokens, CacheWrite1hTokens: u.CacheWrite1hTokens,
		AudioInputTokens: u.AudioInputTokens, AudioOutputTokens: u.AudioOutputTokens, AudioCachedTokens: u.AudioCachedTokens,
		Tools:        toolCalls(u.Tools),
		ReportedCost: u.ReportedCost, ReportedMicro: u.ReportedMicro,
	})
	if !ok {
		return Charge{Micro: r.amount, Estimated: true}
	}
	return Charge{Micro: priced.Micro, Model: priced.Model}
}

func toolCalls(in []profiles.ToolCalls) []pricing.ToolCalls {
	if len(in) == 0 {
		return nil
	}
	out := make([]pricing.ToolCalls, len(in))
	for i, t := range in {
		out[i] = pricing.ToolCalls{Name: t.Name, Calls: t.Calls}
	}
	return out
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Release gives a reservation back without charging anything, for a request
// that was refused after it was taken. It does nothing to one already settled.
func (m *Meter) Release(r *Reservation) {
	if r == nil || !r.done.CompareAndSwap(false, true) {
		return
	}
	r.win.release(r.amount)
}

// Period is a key's spend in one day or month.
type Period struct {
	Label string // 2026-11-11 for a day, 2026-11 for a month
	// CostMicro is what has been charged, EstimatedMicro of it as an estimate.
	CostMicro, EstimatedMicro int64
	// ReservedMicro is held for the requests in flight.
	ReservedMicro int64
	Requests      int64 // requests charged
	Unmetered     int64 // requests forwarded that couldn't be metered, whose cost is unknown
}

// Spent is a key's spend today and this month.
type Spent struct{ Day, Month Period }

// Spend returns a key's spend at now, committed and not yet flushed alike.
func (m *Meter) Spend(keyID string, now time.Time) Spent {
	if v, ok := m.accounts.Load(keyID); ok {
		w := v.(*account).window(now, m.opts.Location)
		return Spent{Day: w.day.period(), Month: w.month.period()}
	}
	day, month := labels(now, m.opts.Location)
	return Spent{Day: Period{Label: day}, Month: Period{Label: month}}
}

func (m *Meter) failedClosed(now time.Time) bool {
	since := m.failingSince.Load()
	return since != 0 && now.UnixNano()-since >= int64(m.opts.FailClosedAfter)
}

func (m *Meter) account(keyID string) *account {
	if v, ok := m.accounts.Load(keyID); ok {
		return v.(*account)
	}
	v, _ := m.accounts.LoadOrStore(keyID, &account{id: keyID, days: map[string]*dayState{}, months: map[string]*bucket{}})
	return v.(*account)
}
