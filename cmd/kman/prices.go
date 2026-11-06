package main

import (
	"slices"
	"sync"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/pricing"
)

// builtinPrices is the embedded price table as admin serves it. admin may not
// import pricing (SPEC §2), so the table is adapted here. It's built once: the
// table never changes while kman runs.
var builtinPrices = sync.OnceValue(func() admin.PriceTable {
	t := pricing.Builtin()
	out := admin.PriceTable{GeneratedAt: t.GeneratedAt(), StaleAfter: pricing.StaleAfter}
	for row := range t.All() {
		out.Models = append(out.Models, priceModel(row))
	}
	return out
})

// publishOverrides puts admin's overrides in force, adapting them because admin
// may not import pricing.
func publishOverrides(prices *pricing.Prices) func([]admin.PriceOverride) error {
	return func(rows []admin.PriceOverride) error {
		overrides := make([]pricing.Override, 0, len(rows))
		for _, o := range rows {
			overrides = append(overrides, pricing.Override{
				UpstreamID: o.UpstreamID, Model: o.Model, MaxOutput: o.MaxOutput,
				Rates: pricing.Rates{
					Input: o.InputMicro, Output: o.OutputMicro,
					Cached: o.CachedMicro, CacheWrite: o.CacheWriteMicro, CacheWrite1h: o.CacheWrite1hMicro,
					AudioInput: o.AudioInputMicro, AudioOutput: o.AudioOutputMicro, AudioCached: o.AudioCachedMicro,
				},
			})
		}
		return prices.SetOverrides(overrides)
	}
}

func priceModel(row pricing.Listed) admin.PriceModel {
	e := row.Entry
	m := admin.PriceModel{
		Preset: row.Preset, Model: row.Model, Mode: e.Mode,
		InputMicro: e.Input, OutputMicro: e.Output, CachedMicro: e.Cached, CacheWriteMicro: e.CacheWrite,
		AudioInputMicro: e.AudioInput, AudioOutputMicro: e.AudioOutput,
		MaxOutput: e.MaxOutput, Reviewed: e.Reviewed, ContextWindow: e.ContextWindow,
		PerMinuteMicro: e.PerMinute, PerTextInputMicro: e.PerTextInput,
	}
	for _, b := range e.Long {
		m.LongContext = append(m.LongContext, admin.PriceBand{AboveTokens: b.Above, InputMicro: b.Input, OutputMicro: b.Output})
	}
	for name := range e.Tiers {
		m.ServiceTiers = append(m.ServiceTiers, name)
	}
	slices.Sort(m.ServiceTiers)
	for tool, fee := range e.ToolFees {
		m.ToolFees = append(m.ToolFees, admin.PriceToolFee{Tool: tool, Micro: fee})
	}
	slices.SortFunc(m.ToolFees, func(a, b admin.PriceToolFee) int {
		switch {
		case a.Tool < b.Tool:
			return -1
		case a.Tool > b.Tool:
			return 1
		}
		return 0
	})
	return m
}
