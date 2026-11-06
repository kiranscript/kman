package pricing

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"time"
)

// Schema is the version of the table's file format that Parse reads.
const Schema = 1

// StaleAfter is how old the table gets before the status summary and the
// dashboard ask for an upgrade. Age never changes enforcement.
const StaleAfter = 60 * 24 * time.Hour

// Rates are token prices in microdollars per million tokens. An optional price
// that is nil bills at the rate it falls back to (see the field), never at
// zero: a nil and a published price of zero mean different things.
type Rates struct {
	Input  int64 `json:"input,omitzero"`
	Output int64 `json:"output,omitzero"`

	Cached       *int64 `json:"cached,omitempty"`         // falls back to Input
	CacheWrite   *int64 `json:"cache_write,omitempty"`    // falls back to Input
	CacheWrite1h *int64 `json:"cache_write_1h,omitempty"` // falls back to CacheWrite
	AudioInput   *int64 `json:"audio_input,omitempty"`    // falls back to Input
	AudioOutput  *int64 `json:"audio_output,omitempty"`   // falls back to Output
	AudioCached  *int64 `json:"audio_cached,omitempty"`   // falls back to AudioInput
}

// Band is a long-context tier: a request whose input is over Above tokens has
// all of its tokens billed at these rates, not only the tokens over it.
type Band struct {
	Above int64 `json:"above"`
	Rates
}

// Schedule is one service tier's rates: the base band, then the long-context
// bands in ascending order.
type Schedule struct {
	Rates
	Long []Band `json:"long,omitempty"`
}

// Entry is what one model costs on one upstream kind, and how much it can
// output in one response.
type Entry struct {
	Mode          string `json:"mode"` // chat, responses, completion, embedding or realtime
	ContextWindow int64  `json:"context,omitzero"`
	MaxOutput     int64  `json:"max_output,omitzero"`
	// Reviewed says MaxOutput came from the reviewed data file, checked
	// against the provider's docs, rather than from the price sources.
	Reviewed bool `json:"reviewed,omitzero"`
	Schedule
	Tiers        map[string]*Schedule `json:"tiers,omitempty"`         // by service tier: batch, flex, priority, fast...
	PerMinute    int64               `json:"per_minute,omitzero"`     // voice: microdollars per minute, billed per second
	PerTextInput int64               `json:"per_text_input,omitzero"` // voice: microdollars per text input
	ToolFees     map[string]int64    `json:"tools,omitempty"`         // microdollars per server-tool call, by tool
}

// Source names one dataset a table was generated from.
type Source struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Data is the table's file format. Global holds models under the id their
// maker's API uses; Presets holds, by preset name, the entries for models whose
// id or price is that preset's own.
type Data struct {
	Schema      int                         `json:"schema"`
	GeneratedAt time.Time                   `json:"generated_at"`
	Sources     []Source                    `json:"sources,omitempty"`
	Global      map[string]Entry            `json:"global,omitempty"`
	Presets     map[string]map[string]Entry `json:"presets,omitempty"`
}

// Table answers what models cost. It is immutable: the request path reads it
// without locks.
type Table struct {
	generatedAt time.Time
	global      map[string]*Entry
	presets     map[string]map[string]*Entry
}

// Parse reads and checks a table file, refusing one that can't be enforced.
func Parse(raw []byte) (*Table, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Data
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("price table: %w", err)
	}
	return New(d)
}

// New checks d and returns its table.
func New(d Data) (*Table, error) {
	if d.Schema != Schema {
		return nil, fmt.Errorf("price table: schema %d, this kman reads %d", d.Schema, Schema)
	}
	if d.GeneratedAt.IsZero() {
		return nil, errors.New("price table: no generated_at")
	}
	t := &Table{generatedAt: d.GeneratedAt, presets: make(map[string]map[string]*Entry, len(d.Presets))}
	var err error
	if t.global, err = section("global", d.Global); err != nil {
		return nil, err
	}
	for name, entries := range d.Presets {
		if t.presets[name], err = section("preset "+name, entries); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func section(where string, entries map[string]Entry) (map[string]*Entry, error) {
	out := make(map[string]*Entry, len(entries))
	for model, e := range entries {
		if err := e.check(); err != nil {
			return nil, fmt.Errorf("price table: %s, model %q: %w", where, model, err)
		}
		out[model] = &e
	}
	return out, nil
}

func (e *Entry) check() error {
	if e.MaxOutput <= 0 && e.Mode != "embedding" && e.PerMinute == 0 {
		return errors.New("no max output")
	}
	if e.PerMinute < 0 || e.PerTextInput < 0 {
		return errors.New("negative price")
	}
	for _, fee := range e.ToolFees {
		if fee < 0 {
			return errors.New("negative price")
		}
	}
	if err := e.Schedule.check(); err != nil {
		return err
	}
	for _, s := range e.Tiers {
		if err := s.check(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Schedule) check() error {
	if err := s.Rates.check(); err != nil {
		return err
	}
	for i, b := range s.Long {
		if i > 0 && b.Above <= s.Long[i-1].Above {
			return errors.New("long-context bands are not ascending")
		}
		if err := b.Rates.check(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Rates) check() error {
	if r.Input < 0 || r.Output < 0 {
		return errors.New("negative price")
	}
	for _, p := range []*int64{r.Cached, r.CacheWrite, r.CacheWrite1h, r.AudioInput, r.AudioOutput, r.AudioCached} {
		if p != nil && *p < 0 {
			return errors.New("negative price")
		}
	}
	return nil
}

// GeneratedAt is when the table was generated.
func (t *Table) GeneratedAt() time.Time { return t.generatedAt }

// Age is how old the table is at now, and never negative.
func (t *Table) Age(now time.Time) time.Duration { return max(now.Sub(t.generatedAt), 0) }

// Stale reports whether the table is past StaleAfter at now. It decides only
// what kman says, never what kman enforces.
func (t *Table) Stale(now time.Time) bool { return t.Age(now) > StaleAfter }

// Lookup returns the entry for model on an upstream built from preset: the
// preset's own entry, then the global one. Ids match exactly: no stripping of
// date suffixes and no fallback by prefix. An empty preset means a custom
// upstream, which sees only the global entries.
func (t *Table) Lookup(preset, model string) (*Entry, bool) {
	if e, ok := t.presets[preset][model]; ok {
		return e, true
	}
	e, ok := t.global[model]
	return e, ok
}

// Listed is one row of the table.
type Listed struct {
	Preset string // empty for a global entry
	Model  string
	Entry  *Entry
}

// All yields every row, the global ones first, then by preset and model.
func (t *Table) All() iter.Seq[Listed] {
	return func(yield func(Listed) bool) {
		for _, preset := range append([]string{""}, slices.Sorted(maps.Keys(t.presets))...) {
			entries := t.global
			if preset != "" {
				entries = t.presets[preset]
			}
			for _, model := range slices.SortedFunc(maps.Keys(entries), func(a, b string) int {
				return cmp.Or(strings.Compare(strings.ToLower(a), strings.ToLower(b)), strings.Compare(a, b))
			}) {
				if !yield(Listed{preset, model, entries[model]}) {
					return
				}
			}
		}
	}
}
