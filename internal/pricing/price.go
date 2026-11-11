package pricing

import (
	"math"
	"math/bits"
	"time"
)

// Request is what's known about a request before it's forwarded.
type Request struct {
	UpstreamID    string // finds its overrides; empty sees only the install's
	Preset, Model string
	MultiplierPPM int64 // the upstream's price multiplier; 1,000,000 is ×1, as is 0
	RequestBytes  int64
	MaxTokens     int64 // the request's max_tokens; 0 when unset
	ServiceTier   string
	Duration      time.Duration // voice: the stretch of session to reserve
}

// Estimate is the size of a reservation, and what it assumed.
type Estimate struct {
	Micro        int64
	InputTokens  int64
	OutputTokens int64  // the output allowance
	ServiceTier  string // the tier it's priced at; empty for the base schedule
}

// Estimate sizes a reservation: request bytes ÷ 4 at the input price, plus the
// output allowance at the output price. The allowance is max_tokens capped at
// the model's max output, or the max output when unset. The long-context band
// comes from the estimated input. With no tier requested, or one the table
// doesn't know, it reserves at the highest known. The upstream's multiplier
// scales it unless the upstream's own override set the price. It reports false
// for a model the table doesn't know.
func (t *Table) Estimate(r Request) (Estimate, bool) {
	e, origin, ok := t.Resolve(r.UpstreamID, r.Preset, r.Model)
	if !ok {
		return Estimate{}, false
	}
	input := max(r.RequestBytes, 0)
	input = input/4 + min(input%4, 1) // a started token counts
	output := e.MaxOutput
	if r.MaxTokens > 0 && r.MaxTokens < output {
		output = r.MaxTokens
	}
	u := Usage{InputTokens: input, OutputTokens: output, Duration: r.Duration}
	sched, tier := e.pick(r.ServiceTier, true, &u)
	micro := multiplied(e.charge(sched, &u), origin, r.MultiplierPPM)
	return Estimate{Micro: micro, InputTokens: input, OutputTokens: output, ServiceTier: tier}, true
}

// Usage is what a response reported. Token counts are disjoint: InputTokens
// holds neither cached, cache-write nor audio tokens.
type Usage struct {
	UpstreamID string // finds its overrides; empty sees only the install's
	Preset     string
	// MultiplierPPM is the upstream's price multiplier: 1,000,000 is ×1, as
	// is 0.
	MultiplierPPM int64
	// Model is the model the response names, or the requested one when it
	// names none. RequestedModel is what to charge when the table doesn't
	// know Model.
	Model, RequestedModel string
	ServiceTier           string // the tier reported; empty when none is

	InputTokens, OutputTokens            int64
	CachedTokens                         int64
	CacheWriteTokens, CacheWrite1hTokens int64
	AudioInputTokens, AudioOutputTokens  int64
	AudioCachedTokens                    int64

	Duration   time.Duration // voice: the session time billed
	TextInputs int64         // voice: the text inputs billed
	Tools      []ToolCalls   // the server-tool calls in the response

	// ReportedCost says the provider reported this response's cost
	// (ReportedMicro, zero included), which is final.
	ReportedCost  bool
	ReportedMicro int64
}

// ToolCalls counts the calls of one server tool, named as an entry's ToolFees
// name it: web_search, file_search, code_interpreter, maps_grounding.
type ToolCalls struct {
	Name  string
	Calls int64
}

// Cost is the charge for a response, and what it was charged as.
type Cost struct {
	Micro       int64
	Model       string // the model charged; empty for a reported cost
	ServiceTier string // the tier charged; empty for the base schedule
	Reported    bool   // the provider reported the cost
}

// Cost prices a response at what the upstream reports: its model when the table
// knows it (else the requested one), the band its actual input falls in, and
// its tier (the base schedule when none is reported, and the highest known when
// the table doesn't know it). The upstream's multiplier scales it unless the
// upstream's own override set the price. A provider-reported cost is final and
// needs no price. It reports false when the table knows neither model.
func (t *Table) Cost(u Usage) (Cost, bool) {
	if u.ReportedCost {
		return Cost{Micro: max(u.ReportedMicro, 0), Reported: true}, true
	}
	model := u.Model
	e, origin, ok := t.Resolve(u.UpstreamID, u.Preset, model)
	if !ok && u.RequestedModel != "" {
		model = u.RequestedModel
		e, origin, ok = t.Resolve(u.UpstreamID, u.Preset, model)
	}
	if !ok {
		return Cost{}, false
	}
	sched, tier := e.pick(u.ServiceTier, false, &u)
	return Cost{Micro: multiplied(e.charge(sched, &u), origin, u.MultiplierPPM), Model: model, ServiceTier: tier}, true
}

// multiplied applies an upstream's price multiplier to a charge. It scales the
// charge, not each rate, which picks the same schedule and costs no allocation.
// A per-upstream override is that upstream's own price, so it stays as it is.
func multiplied(micro int64, origin Origin, ppm int64) int64 {
	if ppm == 0 || origin == OriginUpstreamOverride {
		return micro
	}
	return mulDivCeil(micro, ppm, 1_000_000)
}

// pick chooses the schedule for a named tier. The names default and standard
// mean the base schedule, and so does no name unless unnamedIsHighest. Anything
// else the entry doesn't know is priced at whichever schedule charges u most.
func (e *Entry) pick(name string, unnamedIsHighest bool, u *Usage) (*Schedule, string) {
	if name == "default" || name == "standard" || (name == "" && !unnamedIsHighest) {
		return &e.Schedule, ""
	}
	if s, ok := e.Tiers[name]; ok {
		return s, name
	}
	best, bestName, most := &e.Schedule, "", e.charge(&e.Schedule, u)
	for n, s := range e.Tiers {
		c := e.charge(s, u)
		if c > most || (c == most && bestName != "" && n < bestName) {
			best, bestName, most = s, n, c
		}
	}
	return best, bestName
}

// charge prices u on one schedule of e.
func (e *Entry) charge(s *Schedule, u *Usage) int64 {
	r := s.band(sum(u.InputTokens, u.CachedTokens, u.CacheWriteTokens, u.CacheWrite1hTokens,
		u.AudioInputTokens, u.AudioCachedTokens))
	write := or(r.CacheWrite, r.Input)
	audio := or(r.AudioInput, r.Input)
	total := sum(
		tokens(u.InputTokens, r.Input),
		tokens(u.OutputTokens, r.Output),
		tokens(u.CachedTokens, or(r.Cached, r.Input)),
		tokens(u.CacheWriteTokens, write),
		tokens(u.CacheWrite1hTokens, or(r.CacheWrite1h, write)),
		tokens(u.AudioInputTokens, audio),
		tokens(u.AudioOutputTokens, or(r.AudioOutput, r.Output)),
		tokens(u.AudioCachedTokens, or(r.AudioCached, audio)),
		mulDivCeil(u.Duration.Nanoseconds(), e.PerMinute, time.Minute.Nanoseconds()),
		mulDivCeil(u.TextInputs, e.PerTextInput, 1),
	)
	for _, c := range u.Tools {
		total = sum(total, mulDivCeil(c.Calls, e.ToolFees[c.Name], 1))
	}
	return total
}

// band returns the rates for a request with this much input.
func (s *Schedule) band(input int64) *Rates {
	r := &s.Rates
	for i := range s.Long {
		if input > s.Long[i].Above {
			r = &s.Long[i].Rates
		}
	}
	return r
}

func or(p *int64, fallback int64) int64 {
	if p != nil {
		return *p
	}
	return fallback
}

func tokens(n, perMillion int64) int64 { return mulDivCeil(n, perMillion, 1_000_000) }

// mulDivCeil returns a×b÷c rounded up, as a charge rounds up to the next
// microdollar. Negative arguments count as zero, and a result past int64 stays
// at the largest value rather than wrapping into a small one.
func mulDivCeil(a, b, c int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return math.MaxInt64
	}
	q, r := bits.Div64(hi, lo, uint64(c))
	if r > 0 {
		q++
	}
	if q > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(q)
}

// sum adds non-negative numbers, staying at the largest value on overflow.
func sum(ns ...int64) int64 {
	var total int64
	for _, n := range ns {
		n = max(n, 0)
		if total > math.MaxInt64-n {
			return math.MaxInt64
		}
		total += n
	}
	return total
}
