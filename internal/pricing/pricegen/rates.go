package main

import (
	"cmp"
	"maps"
	"math"
	"slices"

	"github.com/kirank55/kman/internal/pricing"
)

// field is one kind of token price.
type field int

const (
	fInput field = iota
	fOutput
	fCached
	fWrite
	fWrite1h
	fAudioIn
	fAudioOut
	fAudioCached
	nFields
)

var fieldNames = [nFields]string{"input", "output", "cached", "cache_write", "cache_write_1h", "audio_input", "audio_output", "audio_cached"}

// curve is one price as a function of a request's input tokens: a base value
// and the values that replace it over each threshold. A source can state a
// long-context price with no base, so the base is a pointer.
type curve struct {
	base  *int64
	steps map[int64]int64 // threshold → price for input over it
}

func (c curve) empty() bool { return c.base == nil && len(c.steps) == 0 }

// at returns the price for a request whose input is just over threshold, or
// nil if the source states none there. A threshold below all the steps, and
// threshold -1 in particular, gives the base.
func (c curve) at(threshold int64) *int64 {
	v, best := c.base, int64(-1)
	for above, price := range c.steps {
		if above <= threshold && above > best {
			price := price
			v, best = &price, above
		}
	}
	return v
}

// schedule is one service tier's prices, field by field. Fields are separate
// because sources state them separately.
type schedule [nFields]curve

// row is what one source says about one model.
type row struct {
	mode            string // empty when the source doesn't say
	context, maxOut int64
	base            schedule
	tiers           map[string]*schedule
	perMinute       int64
	perText         int64
	tools           map[string]int64
}

func (r *row) tier(name string) *schedule {
	if r.tiers == nil {
		r.tiers = map[string]*schedule{}
	}
	if r.tiers[name] == nil {
		r.tiers[name] = &schedule{}
	}
	return r.tiers[name]
}

// inherit fills the fields each tier doesn't state from the base: a source
// listing only a flex input price still bills flex output at the normal rate.
func (r *row) inherit() {
	for _, t := range r.tiers {
		for f := range nFields {
			if t[f].empty() {
				t[f] = r.base[f]
			}
		}
	}
}

// thresholds returns the thresholds of a schedule set, ascending.
func thresholds(scheds ...*schedule) []int64 {
	set := map[int64]bool{}
	for _, s := range scheds {
		for _, c := range s {
			for above := range c.steps {
				set[above] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// mergeCurves keeps the higher price at every point of the input range.
func mergeCurves(a, b curve) curve {
	if a.empty() {
		return b
	}
	if b.empty() {
		return a
	}
	out := curve{base: higher(a.base, b.base)}
	for _, c := range []curve{a, b} {
		for above := range c.steps {
			if v := higher(a.at(above), b.at(above)); v != nil {
				if out.steps == nil {
					out.steps = map[int64]int64{}
				}
				out.steps[above] = *v
			}
		}
	}
	return out
}

func higher(a, b *int64) *int64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	v := max(*a, *b)
	return &v
}

// toSchedule renders the prices as the table stores them, without bands that
// repeat the band below.
func toSchedule(s *schedule) pricing.Schedule {
	rates := func(threshold int64) pricing.Rates {
		var r pricing.Rates
		for f := range nFields {
			v := s[f].at(threshold)
			switch f {
			case fInput:
				r.Input = deref(v)
			case fOutput:
				r.Output = deref(v)
			case fCached:
				r.Cached = v
			case fWrite:
				r.CacheWrite = v
			case fWrite1h:
				r.CacheWrite1h = v
			case fAudioIn:
				r.AudioInput = v
			case fAudioOut:
				r.AudioOutput = v
			case fAudioCached:
				r.AudioCached = v
			}
		}
		return r
	}
	out := pricing.Schedule{Rates: rates(-1)}
	prev := out.Rates
	for _, above := range thresholds(s) {
		r := rates(above)
		if sameRates(r, prev) {
			continue
		}
		out.Long = append(out.Long, pricing.Band{Above: above, Rates: r})
		prev = r
	}
	return out
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func sameRates(a, b pricing.Rates) bool {
	eq := func(x, y *int64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Input == b.Input && a.Output == b.Output && eq(a.Cached, b.Cached) && eq(a.CacheWrite, b.CacheWrite) &&
		eq(a.CacheWrite1h, b.CacheWrite1h) && eq(a.AudioInput, b.AudioInput) && eq(a.AudioOutput, b.AudioOutput) && eq(a.AudioCached, b.AudioCached)
}

// micro converts a price in dollars per scale units to microdollars per
// million tokens, or to microdollars with scale 1e6.
func micro(dollars, scale float64) int64 {
	return int64(math.Round(dollars * scale))
}

const (
	perToken = 1e12 // $/token → µ$/Mtok
	perMtok  = 1e6  // $/Mtok → µ$/Mtok
)

func sortedKeys[V any](m map[string]V) []string {
	return slices.SortedFunc(maps.Keys(m), cmp.Compare[string])
}
