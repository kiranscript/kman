package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/kirank55/kman/internal/pricing"
)

// Flag is something CI points a reviewer at.
type Flag struct {
	Kind          string // changed, added, removed or max_output_equals_context
	Preset, Model string // Preset is empty for a global entry
	Field         string // for changed: which value
	Old, New      string // for changed
	Fails         bool   // the check fails on it, rather than asking for a look
}

// check compares a table with the one it replaces. A reviewed max output equal
// to the context window fails unless the reviewed file explains it: the review
// exists to catch exactly that. Everything else is for a person to look at.
func check(base, head pricing.Data, reviewed map[key]reviewedEntry) []Flag {
	var flags []Flag
	checkSection := func(preset string, b, h map[string]pricing.Entry) {
		for _, model := range sortedKeys(h) {
			e := h[model]
			if e.MaxOutput > 0 && e.MaxOutput == e.ContextWindow {
				f := Flag{Kind: "max_output_equals_context", Preset: preset, Model: model}
				if e.Reviewed {
					f.Fails = reviewedFor(reviewed, preset, model).EqualsContext == ""
				}
				flags = append(flags, f)
			}
			old, ok := b[model]
			if !ok {
				flags = append(flags, Flag{Kind: "added", Preset: preset, Model: model})
				continue
			}
			flags = append(flags, changes(preset, model, old, e)...)
		}
		for _, model := range sortedKeys(b) {
			if _, ok := h[model]; !ok {
				flags = append(flags, Flag{Kind: "removed", Preset: preset, Model: model})
			}
		}
	}
	checkSection("", base.Global, head.Global)
	sections := map[string]bool{}
	for p := range base.Presets {
		sections[p] = true
	}
	for p := range head.Presets {
		sections[p] = true
	}
	for _, preset := range slices.Sorted(maps.Keys(sections)) {
		checkSection(preset, base.Presets[preset], head.Presets[preset])
	}
	return flags
}

func reviewedFor(reviewed map[key]reviewedEntry, preset, model string) reviewedEntry {
	if preset != "" {
		return reviewed[key{preset, model}]
	}
	for p := range globalPresets {
		if e, ok := reviewed[key{p, model}]; ok {
			return e
		}
	}
	return reviewedEntry{}
}

// changes compares two entries value by value, as the table file states them.
func changes(preset, model string, old, new pricing.Entry) []Flag {
	a, b := fields(old), fields(new)
	names := map[string]bool{}
	for n := range a {
		names[n] = true
	}
	for n := range b {
		names[n] = true
	}
	var out []Flag
	for _, n := range slices.Sorted(maps.Keys(names)) {
		if !bytes.Equal(a[n], b[n]) {
			out = append(out, Flag{Kind: "changed", Preset: preset, Model: model, Field: n, Old: string(a[n]), New: string(b[n])})
		}
	}
	return out
}

func fields(e pricing.Entry) map[string]json.RawMessage {
	raw, err := json.Marshal(e)
	if err != nil {
		panic(err) // an Entry always marshals
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	return m
}

// writeFlags prints flags as the Markdown a reviewer reads, in the weekly pull
// request and in CI's job summary.
func writeFlags(w io.Writer, flags []Flag) {
	by := func(kind string) []Flag {
		return slices.DeleteFunc(slices.Clone(flags), func(f Flag) bool { return f.Kind != kind })
	}
	section := func(f Flag) string {
		if f.Preset == "" {
			return "global"
		}
		return f.Preset
	}
	changed := by("changed")
	if len(changed) == 0 {
		fmt.Fprintln(w, "No value changed.")
	} else {
		fmt.Fprintf(w, "### Values changed (%d)\n\n| Section | Model | Value | Old | New |\n|---|---|---|---|---|\n", len(changed))
		for _, f := range changed {
			fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s |\n", section(f), f.Model, f.Field, cell(f.Old), cell(f.New))
		}
	}
	list := func(kind, title string) {
		fs := by(kind)
		if len(fs) == 0 {
			return
		}
		fmt.Fprintf(w, "\n### %s (%d)\n\n", title, len(fs))
		for _, f := range fs {
			note := ""
			if f.Fails {
				note = " **(reviewed, not explained: fails)**"
			}
			fmt.Fprintf(w, "- %s `%s`%s\n", section(f), f.Model, note)
		}
	}
	list("added", "Models added")
	list("removed", "Models removed")
	list("max_output_equals_context", "Max output equal to the context window")
}

// cell keeps a value on one table row.
func cell(s string) string {
	if s == "" {
		return "(none)"
	}
	return strings.ReplaceAll(s, "|", "\\|")
}
