package admin

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kirank55/kman/internal/store"
)

// PriceOverrideRates are an override's optional prices, per million tokens in
// microdollars. Absent is not zero: it bills at the price it falls back to.
type PriceOverrideRates struct {
	CachedMicro       *int64 `json:"cached_micro,omitempty" doc:"Cached input. Absent: billed at the input price."`
	CacheWriteMicro   *int64 `json:"cache_write_micro,omitempty" doc:"Cache writes. Absent: billed at the input price."`
	CacheWrite1hMicro *int64 `json:"cache_write_1h_micro,omitempty" doc:"One-hour cache writes. Absent: billed at the cache write price."`
	AudioInputMicro   *int64 `json:"audio_input_micro,omitempty" doc:"Audio input. Absent: billed at the input price."`
	AudioOutputMicro  *int64 `json:"audio_output_micro,omitempty" doc:"Audio output. Absent: billed at the output price."`
	AudioCachedMicro  *int64 `json:"audio_cached_micro,omitempty" doc:"Cached audio input. Absent: billed at the audio input price."`
}

// PriceOverride is an operator's price for one model, install-wide or on one
// upstream. It wins over the built-in entries until it is removed, and replaces
// a whole entry: the built-in's long-context bands, service tiers and per-call
// fees do not apply under it.
type PriceOverride struct {
	Upstream    string `json:"upstream,omitempty" doc:"The upstream it applies to, by name. Absent for an install-wide override."`
	UpstreamID  string `json:"upstream_id,omitempty"`
	Model       string `json:"model" doc:"The model id, matched exactly."`
	InputMicro  int64  `json:"input_micro"`
	OutputMicro int64  `json:"output_micro"`
	MaxOutput   int64  `json:"max_output" doc:"The most tokens one response can output, which sizes reservations."`
	PriceOverrideRates
	Hides     []string  `json:"hides,omitempty" doc:"The sections of the price table whose built-in entry for this model it hides: global or a preset's name."`
	Actor     string    `json:"actor" doc:"Who last set it."`
	UpdatedAt time.Time `json:"updated_at"`
}

// PriceOverrideSet sets one override. Setting the prices it already has
// changes nothing and records nothing.
type PriceOverrideSet struct {
	Upstream    string `json:"upstream,omitempty" doc:"The upstream to price it on, by name or id. Absent for the whole install."`
	Model       string `json:"model" doc:"The model id, matched exactly."`
	InputMicro  *int64 `json:"input_micro" required:"true" doc:"Input, in microdollars per million tokens."`
	OutputMicro *int64 `json:"output_micro" required:"true" doc:"Output, in microdollars per million tokens."`
	MaxOutput   *int64 `json:"max_output" required:"true" doc:"The most tokens one response can output, which sizes reservations."`
	PriceOverrideRates
}

// PriceOverrideRef names one override.
type PriceOverrideRef struct {
	Upstream string `json:"-" query:"upstream" doc:"The upstream it is on, by name or id. Absent for an install-wide override."`
	Model    string `json:"-" query:"model" doc:"The model id, matched exactly."`
}

// PriceOverrideImport sets many overrides in one transaction: one that is
// refused refuses them all. Overrides it does not name stay as they are.
type PriceOverrideImport struct {
	Overrides []PriceOverrideSet `json:"overrides"`
}

// PriceOverrideImportResult says what an import did.
type PriceOverrideImportResult struct {
	Changed   int             `json:"changed" doc:"How many it added or changed, each with its own audit entry."`
	Unchanged int             `json:"unchanged" doc:"How many already had these prices."`
	Overrides []PriceOverride `json:"overrides" doc:"Every override it named, as it now stands."`
}

// maxModelID is far longer than any model id, and bounds what's stored.
const maxModelID = 256

func checkModelID(model string) error {
	switch {
	case model == "":
		return errorf(CodeInvalid, "an override needs a model")
	case len(model) > maxModelID:
		return errorf(CodeInvalid, "the model id is over %d bytes", maxModelID)
	case strings.ContainsFunc(model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return errorf(CodeInvalid, "the model id %q holds a space or a control character", model)
	}
	return nil
}

// check refuses an override that can't be enforced. A price of zero is fine, as
// for a local model; no max output would leave a reservation unsized.
func (r PriceOverrideSet) check() error {
	if err := checkModelID(r.Model); err != nil {
		return err
	}
	for _, p := range []struct {
		name string
		v    *int64
	}{{"input_micro", r.InputMicro}, {"output_micro", r.OutputMicro}, {"max_output", r.MaxOutput}} {
		if p.v == nil {
			return errorf(CodeInvalid, "the override of %s needs %s", r.Model, p.name)
		}
	}
	if *r.MaxOutput <= 0 {
		return errorf(CodeInvalid, "the override of %s needs a max_output of at least 1", r.Model)
	}
	for _, p := range []struct {
		name string
		v    *int64
	}{
		{"input_micro", r.InputMicro}, {"output_micro", r.OutputMicro},
		{"cached_micro", r.CachedMicro}, {"cache_write_micro", r.CacheWriteMicro}, {"cache_write_1h_micro", r.CacheWrite1hMicro},
		{"audio_input_micro", r.AudioInputMicro}, {"audio_output_micro", r.AudioOutputMicro}, {"audio_cached_micro", r.AudioCachedMicro},
	} {
		if p.v != nil && *p.v < 0 {
			return errorf(CodeInvalid, "the override of %s has a negative %s", r.Model, p.name)
		}
	}
	return nil
}

// builtinSections is the sections of the price table holding each model: global
// first, then presets, as the table lists them.
type builtinSections map[string][]string

func sectionsOf(t PriceTable) builtinSections {
	out := builtinSections{}
	for _, m := range t.Models {
		section := m.Preset
		if section == "" {
			section = "global"
		}
		out[m.Model] = append(out[m.Model], section)
	}
	return out
}

// sections is the built-in table's sections, or none on a service without one.
func (s *Service) sections() builtinSections {
	if s.prices == nil {
		return nil
	}
	return sectionsOf(s.prices())
}

// hides names what an override of model hides. An install-wide one hides every
// built-in entry of the model. One on an upstream hides the entry that upstream
// would otherwise use: its preset's, then the global one.
func (b builtinSections) hides(scope, preset, model string) []string {
	found := b[model]
	if scope == store.InstallScope {
		return slices.Clone(found)
	}
	for _, want := range []string{preset, "global"} {
		if want != "" && slices.Contains(found, want) {
			return []string{want}
		}
	}
	return nil
}

// overrideRow is r as it is stored in scope.
func overrideRow(r PriceOverrideSet, scope, actor string, now time.Time) (store.PriceOverride, error) {
	extra, err := json.Marshal(r.PriceOverrideRates)
	if err != nil {
		return store.PriceOverride{}, fmt.Errorf("admin: marshal override: %w", err)
	}
	return store.PriceOverride{Scope: scope, Model: r.Model, Input: *r.InputMicro, Output: *r.OutputMicro, MaxOutput: *r.MaxOutput,
		ExtraJSON: string(extra), Actor: actor, UpdatedAt: now}, nil
}

// overrideDTO is the row as the API shows it, on upstream u unless it is
// install-wide.
func overrideDTO(o store.PriceOverride, u store.Upstream, sections builtinSections) (PriceOverride, error) {
	out := PriceOverride{Model: o.Model, InputMicro: o.Input, OutputMicro: o.Output, MaxOutput: o.MaxOutput,
		Hides: sections.hides(o.Scope, u.Preset, o.Model), Actor: o.Actor, UpdatedAt: o.UpdatedAt}
	if o.Scope != store.InstallScope {
		out.Upstream, out.UpstreamID = u.Name, o.Scope
	}
	if err := json.Unmarshal([]byte(o.ExtraJSON), &out.PriceOverrideRates); err != nil {
		return PriceOverride{}, fmt.Errorf("admin: override of %s: %w", o.Model, err)
	}
	return out, nil
}

// overrideScope finds the scope a reference to an upstream names, and the
// upstream: the zero one for the whole install.
func overrideScope(tx *store.Tx, ref string) (string, store.Upstream, error) {
	if ref == "" {
		return store.InstallScope, store.Upstream{}, nil
	}
	u, err := upstreamByRef(tx, ref)
	if err != nil {
		return "", store.Upstream{}, err
	}
	return u.ID, u, nil
}

// listedOverrides is every override, by scope and model, that reaches preset
// (any, when empty) and has a model id containing model, which is lower case.
func listedOverrides(tx *store.Tx, sections builtinSections, preset, model string) ([]PriceOverride, error) {
	rows, err := tx.PriceOverrides()
	if err != nil {
		return nil, err
	}
	upstreams, err := tx.Upstreams()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.Upstream, len(upstreams))
	for _, u := range upstreams {
		byID[u.ID] = u
	}
	out := []PriceOverride{}
	for _, o := range rows {
		u := byID[o.Scope]
		if !strings.Contains(strings.ToLower(o.Model), model) {
			continue
		}
		if o.Scope != store.InstallScope && preset != "" && preset != cmp.Or(u.Preset, "global") {
			continue
		}
		dto, err := overrideDTO(o, u, sections)
		if err != nil {
			return nil, err
		}
		out = append(out, dto)
	}
	return out, nil
}

// allOverrides is every override, never nil, for the request path, which needs
// no record of what they hide.
func allOverrides(tx *store.Tx) ([]PriceOverride, error) {
	return listedOverrides(tx, nil, "", "")
}

func overrideTarget(scope, model string) string { return scope + "/" + model }

// putOverride sets one override in a transaction. It returns it as it stands
// and the audit entry, nil if it already had these prices.
func putOverride(tx *store.Tx, sections builtinSections, r PriceOverrideSet, actor string, now time.Time) (PriceOverride, *audit, error) {
	scope, u, err := overrideScope(tx, r.Upstream)
	if err != nil {
		return PriceOverride{}, nil, err
	}
	row, err := overrideRow(r, scope, actor, now)
	if err != nil {
		return PriceOverride{}, nil, err
	}
	entry := &audit{action: "price_override.set", kind: "price_override", id: overrideTarget(scope, r.Model)}
	cur, err := tx.PriceOverride(scope, r.Model)
	switch {
	case err == nil:
		dto, err := overrideDTO(cur, u, sections)
		if err != nil {
			return PriceOverride{}, nil, err
		}
		if cur.Input == row.Input && cur.Output == row.Output && cur.MaxOutput == row.MaxOutput && cur.ExtraJSON == row.ExtraJSON {
			return dto, nil, nil
		}
		entry.before = dto
	case isNotFound(err):
	default:
		return PriceOverride{}, nil, err
	}
	stored, err := tx.PutPriceOverride(row)
	if err != nil {
		return PriceOverride{}, nil, err
	}
	dto, err := overrideDTO(stored, u, sections)
	if err != nil {
		return PriceOverride{}, nil, err
	}
	entry.after = dto
	return dto, entry, nil
}

// SetPriceOverride sets one override and audits the change.
func (s *Service) SetPriceOverride(ctx context.Context, req PriceOverrideSet) (PriceOverride, error) {
	got, err := s.ImportPriceOverrides(ctx, PriceOverrideImport{Overrides: []PriceOverrideSet{req}})
	if err != nil {
		return PriceOverride{}, err
	}
	return got.Overrides[0], nil
}

// ImportPriceOverrides sets many overrides, all or none, auditing each change.
func (s *Service) ImportPriceOverrides(ctx context.Context, req PriceOverrideImport) (PriceOverrideImportResult, error) {
	c, err := caller(ctx)
	if err != nil {
		return PriceOverrideImportResult{}, err
	}
	if len(req.Overrides) == 0 {
		return PriceOverrideImportResult{}, errorf(CodeInvalid, "there are no overrides to set")
	}
	for _, r := range req.Overrides {
		if err := r.check(); err != nil {
			return PriceOverrideImportResult{}, err
		}
	}
	sections := s.sections()
	out := PriceOverrideImportResult{}
	fx := &effects{}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		var entries []audit
		seen := map[string]bool{}
		for _, r := range req.Overrides {
			dto, entry, err := putOverride(tx, sections, r, c.Actor, now)
			if err != nil {
				return nil, err
			}
			key := overrideTarget(cmp.Or(dto.UpstreamID, store.InstallScope), dto.Model)
			if seen[key] {
				return nil, errorf(CodeInvalid, "the override of %s is given twice", dto.Model)
			}
			seen[key] = true
			out.Overrides = append(out.Overrides, dto)
			if entry != nil {
				entries = append(entries, *entry)
			}
		}
		out.Changed, out.Unchanged = len(entries), len(req.Overrides)-len(entries)
		if len(entries) > 0 {
			var err error
			if fx.overrides, err = allOverrides(tx); err != nil {
				return nil, err
			}
		}
		return entries, nil
	})
	if err != nil {
		return PriceOverrideImportResult{}, err
	}
	return out, nil
}

// RemovePriceOverride removes one override and audits it, returning what it
// was. The model goes back to whatever is next in the lookup order.
func (s *Service) RemovePriceOverride(ctx context.Context, req PriceOverrideRef) (PriceOverride, error) {
	if err := checkModelID(req.Model); err != nil {
		return PriceOverride{}, err
	}
	sections := s.sections()
	var out PriceOverride
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, _ time.Time) ([]audit, error) {
		scope, u, err := overrideScope(tx, req.Upstream)
		if err != nil {
			return nil, err
		}
		row, err := tx.PriceOverride(scope, req.Model)
		if err != nil {
			return nil, fromStore(err, "price override of "+req.Model)
		}
		if out, err = overrideDTO(row, u, sections); err != nil {
			return nil, err
		}
		if err := tx.DeletePriceOverride(scope, req.Model); err != nil {
			return nil, fromStore(err, "price override of "+req.Model)
		}
		if fx.overrides, err = allOverrides(tx); err != nil {
			return nil, err
		}
		return []audit{{action: "price_override.delete", kind: "price_override", id: overrideTarget(scope, req.Model), before: out}}, nil
	})
	return out, err
}

// removeOverridesOf removes an upstream's overrides as the upstream goes, since
// its id is never used again, and returns their audit entries.
func (s *Service) removeOverridesOf(tx *store.Tx, fx *effects, u store.Upstream) ([]audit, error) {
	rows, err := tx.PriceOverrides()
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(o store.PriceOverride) bool { return o.Scope != u.ID })
	if len(rows) == 0 {
		return nil, nil
	}
	sections := s.sections()
	var entries []audit
	for _, o := range rows {
		before, err := overrideDTO(o, u, sections)
		if err != nil {
			return nil, err
		}
		if err := tx.DeletePriceOverride(o.Scope, o.Model); err != nil {
			return nil, fromStore(err, "price override of "+o.Model)
		}
		entries = append(entries, audit{action: "price_override.delete", kind: "price_override", id: overrideTarget(o.Scope, o.Model), before: before})
	}
	if fx.overrides, err = allOverrides(tx); err != nil {
		return nil, err
	}
	return entries, nil
}
