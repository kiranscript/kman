package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/kirank55/kman/internal/pricing"
)

// globalPresets are the presets whose rows are the table's global section.
var globalPresets = map[string]bool{"openai": true, "anthropic": true}

// inputs is every file a table is generated from.
type inputs struct {
	litellm, genai, modelsdev, reviewed []byte
}

// reviewedEntry is one line of the reviewed data file: a model's max output,
// checked by a person against the provider's docs.
type reviewedEntry struct {
	MaxOutput int64  `json:"max_output"`
	Checked   string `json:"checked"` // the date of the check
	Source    string `json:"source"`  // the page it was checked against
	// EqualsContext explains why this max output is right even though it
	// equals the context window, which CI otherwise refuses for a reviewed
	// model. Usually: the provider publishes no separate limit.
	EqualsContext string `json:"equals_context,omitempty"`
}

var isDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func parseReviewed(raw []byte) (map[key]reviewedEntry, error) {
	var doc map[string]map[string]reviewedEntry
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("reviewed file: %w", err)
	}
	known := map[string]bool{}
	for _, preset := range litellmProviders {
		known[preset] = true
	}
	out := map[key]reviewedEntry{}
	for preset, models := range doc {
		if !known[preset] {
			return nil, fmt.Errorf("reviewed file: %q is not an LLM preset", preset)
		}
		for model, e := range models {
			if e.MaxOutput <= 0 || !isDate.MatchString(e.Checked) || e.Source == "" {
				return nil, fmt.Errorf("reviewed file: %s/%s needs max_output, checked (a date) and source", preset, model)
			}
			out[key{preset, model}] = e
		}
	}
	return out, nil
}

// Disagreement is a value the sources state differently: a person reviews each
// against the provider's page (ADR 0007).
type Disagreement struct {
	Preset, Model string
	Tier          string // empty for the base schedule
	Field         string // a price, with its threshold when over one, or max_output
	Values        map[string]int64
	Kept          int64
}

// report is what a person reviews besides the table.
type report struct {
	Disagreements   []Disagreement
	Dropped         []string // models the sources list that can't be enforced
	ReviewedMissing []string // reviewed models no source prices
}

// generate merges the datasets into a table, keeping the conservative value
// wherever they differ, and takes max output from the reviewed file where it
// has the model.
func generate(in inputs, now time.Time) (pricing.Data, *report, error) {
	reviewed, err := parseReviewed(in.reviewed)
	if err != nil {
		return pricing.Data{}, nil, err
	}
	all, err := parseAll(in, now)
	if err != nil {
		return pricing.Data{}, nil, err
	}
	names := slices.Sorted(maps.Keys(all))
	excluded := map[key]bool{}
	union := map[key]bool{}
	for _, p := range all {
		maps.Copy(excluded, p.excluded)
		for k := range p.rows {
			union[k] = true
		}
	}

	d := pricing.Data{
		Schema: pricing.Schema, GeneratedAt: now.UTC().Truncate(time.Second),
		Global: map[string]pricing.Entry{}, Presets: map[string]map[string]pricing.Entry{},
		Sources: []pricing.Source{
			{Name: "litellm", URL: urlLiteLLM, SHA256: sum(in.litellm)},
			{Name: "genai-prices", URL: urlGenAI, SHA256: sum(in.genai)},
			{Name: "models.dev", URL: urlModelsDev, SHA256: sum(in.modelsdev)},
		},
	}
	seen := map[key]map[string]*row{}
	merged := map[key]*row{}
	for k := range union {
		if excluded[k] {
			continue
		}
		seen[k] = map[string]*row{}
		for _, name := range names {
			if row := all[name].rows[k]; row != nil {
				seen[k][name] = row
				if merged[k] == nil {
					merged[k] = row
				} else {
					merged[k] = mergeRows(merged[k], row)
				}
			}
		}
	}
	for _, p := range all {
		shareLimits(merged, p.aliases)
	}

	r := &report{}
	for _, k := range slices.SortedFunc(maps.Keys(merged), compareKeys) {
		merged := merged[k]
		e, problem := toEntry(merged)
		rev, isReviewed := reviewed[k]
		if isReviewed {
			e.MaxOutput, e.Reviewed = rev.MaxOutput, true
			problem = priceProblem(merged)
		}
		if problem != "" {
			r.Dropped = append(r.Dropped, fmt.Sprintf("%s/%s: %s", k.preset, k.model, problem))
			continue
		}
		r.Disagreements = append(r.Disagreements, disagreements(k, seen[k], merged, e.MaxOutput)...)
		if globalPresets[k.preset] {
			d.Global[k.model] = e
			continue
		}
		if d.Presets[k.preset] == nil {
			d.Presets[k.preset] = map[string]pricing.Entry{}
		}
		d.Presets[k.preset][k.model] = e
	}
	for _, k := range slices.SortedFunc(maps.Keys(reviewed), compareKeys) {
		if !union[k] || excluded[k] {
			r.ReviewedMissing = append(r.ReviewedMissing, k.preset+"/"+k.model)
		}
	}
	return d, r, nil
}

// shareLimits gives each id of a model the limits another of its ids has, where
// a source lists a dated id or alias without any.
func shareLimits(merged map[key]*row, aliases [][]key) {
	for _, group := range aliases {
		var context, maxOut int64
		for _, k := range group {
			if r := merged[k]; r != nil {
				context, maxOut = max(context, r.context), max(maxOut, r.maxOut)
			}
		}
		for _, k := range group {
			if r := merged[k]; r != nil && (r.context == 0 || r.maxOut == 0) {
				filled := *r // rows are shared between the ids of a model
				filled.context, filled.maxOut = cmp.Or(r.context, context), cmp.Or(r.maxOut, maxOut)
				merged[k] = &filled
			}
		}
	}
}

func compareKeys(a, b key) int {
	return cmp.Or(cmp.Compare(a.preset, b.preset), cmp.Compare(a.model, b.model))
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// mergeRows keeps the conservative value of everything two rows state: the
// higher price at every input size, the larger limit.
func mergeRows(a, b *row) *row {
	out := &row{
		mode:      cmp.Or(a.mode, b.mode),
		context:   max(a.context, b.context),
		maxOut:    max(a.maxOut, b.maxOut),
		perMinute: max(a.perMinute, b.perMinute),
		perText:   max(a.perText, b.perText),
	}
	for f := range nFields {
		out.base[f] = mergeCurves(a.base[f], b.base[f])
	}
	for _, src := range []*row{a, b} {
		for name, fee := range src.tools {
			out.addTool(name, fee)
		}
		for name, t := range src.tiers {
			into := out.tier(name)
			for f := range nFields {
				into[f] = mergeCurves(into[f], t[f])
			}
		}
	}
	return out
}

// priceProblem says why a row can't price a request. A row with no output price
// would reserve nothing for its output, so a cap could be overshot by the whole
// allowance.
func priceProblem(r *row) string {
	switch {
	case r.perMinute > 0:
		return ""
	case r.base[fInput].base == nil:
		return "no price"
	case r.base[fOutput].base == nil && r.mode != "embedding":
		return "no output price"
	}
	return ""
}

// toEntry renders a merged row, or says why it can't be enforced.
func toEntry(r *row) (pricing.Entry, string) {
	e := pricing.Entry{
		Mode: cmp.Or(r.mode, "chat"), ContextWindow: r.context, MaxOutput: r.maxOut,
		Schedule: toSchedule(&r.base), PerMinute: r.perMinute, PerTextInput: r.perText,
		ToolFees: r.tools,
	}
	if r.perMinute > 0 && r.mode == "" {
		e.Mode = "realtime"
	}
	for name, t := range r.tiers {
		if e.Tiers == nil {
			e.Tiers = map[string]*pricing.Schedule{}
		}
		s := toSchedule(t)
		e.Tiers[name] = &s
	}
	if p := priceProblem(r); p != "" {
		return e, p
	}
	if e.MaxOutput <= 0 && e.Mode != "embedding" && e.PerMinute == 0 {
		return e, "no max output"
	}
	return e, ""
}

// disagreements lists where the sources that state a value state it
// differently. The max output is the source's, whatever the reviewed file says:
// a person checking the file wants to see how far the sources are off.
func disagreements(k key, seen map[string]*row, merged *row, keptMax int64) []Disagreement {
	var out []Disagreement
	add := func(tier, field string, values map[string]int64, kept int64) {
		distinct := map[int64]bool{}
		for _, v := range values {
			distinct[v] = true
		}
		if len(distinct) > 1 {
			out = append(out, Disagreement{Preset: k.preset, Model: k.model, Tier: tier, Field: field, Values: values, Kept: kept})
		}
	}
	limits := map[string]int64{}
	for name, r := range seen {
		if r.maxOut > 0 {
			limits[name] = r.maxOut
		}
	}
	add("", "max_output", limits, keptMax)

	compare := func(tier string, get func(*row) *schedule) {
		var all []*schedule
		for _, r := range seen {
			if s := get(r); s != nil {
				all = append(all, s)
			}
		}
		for _, threshold := range append([]int64{-1}, thresholds(all...)...) {
			for f := range nFields {
				values := map[string]int64{}
				for name, r := range seen {
					if s := get(r); s != nil {
						if v := s[f].at(threshold); v != nil {
							values[name] = *v
						}
					}
				}
				var kept int64
				for _, v := range values {
					kept = max(kept, v)
				}
				label := fieldNames[f]
				if threshold >= 0 {
					label = fmt.Sprintf("%s over %d", label, threshold)
				}
				add(tier, label, values, kept)
			}
		}
	}
	compare("", func(r *row) *schedule { return &r.base })
	for _, tier := range sortedKeys(merged.tiers) {
		compare(tier, func(r *row) *schedule { return r.tiers[tier] })
	}
	return out
}

// encode writes a table with one model per line, so its diffs read as a list of
// changed models.
func encode(d pricing.Data) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n\"schema\": %d,\n", d.Schema)
	generated, err := json.Marshal(d.GeneratedAt)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "\"generated_at\": %s,\n", generated)
	sources, err := json.Marshal(d.Sources)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "\"sources\": %s,\n\"global\": ", sources)
	if err := encodeSection(&b, d.Global); err != nil {
		return nil, err
	}
	b.WriteString(",\n\"presets\": {")
	for i, preset := range sortedKeys(d.Presets) {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(preset)
		fmt.Fprintf(&b, "\n%s: ", name)
		if err := encodeSection(&b, d.Presets[preset]); err != nil {
			return nil, err
		}
	}
	b.WriteString("\n}\n}\n")
	return b.Bytes(), nil
}

func encodeSection(b *bytes.Buffer, entries map[string]pricing.Entry) error {
	b.WriteByte('{')
	for i, model := range sortedKeys(entries) {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(model)
		if err != nil {
			return err
		}
		e, err := json.Marshal(entries[model])
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "\n%s: %s", name, e)
	}
	b.WriteString("\n}")
	return nil
}
