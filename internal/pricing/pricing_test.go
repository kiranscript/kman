package pricing_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/pricing"
)

// sample prices, written as the provider pages state them (SPEC §8):
//
//	flat: $2 in, $8 out, $0.50 cached, 1,000 max output
//	long: $2.50 in, $15 out; over 272k: $5 in, $22.50 out; $1.25/$7.50 flex
//	voice: $0.05 per minute, $0.004 per text input
//	on the "cheap" preset, flat is $1 in, $4 out
const sample = `{
	"schema": 1,
	"generated_at": "2026-11-01T00:00:00Z",
	"global": {
		"flat": {"mode": "chat", "context": 8000, "max_output": 1000, "input": 2000000, "output": 8000000, "cached": 500000},
		"modal": {"mode": "chat", "max_output": 1000, "input": 4000000, "output": 24000000, "cached": 400000,
			"cache_write": 5000000, "cache_write_1h": 8000000,
			"audio_input": 32000000, "audio_output": 64000000, "audio_cached": 1000000,
			"tools": {"web_search": 10000}},
		"long": {"mode": "chat", "max_output": 128000, "input": 2500000, "output": 15000000, "cached": 250000,
			"long": [{"above": 272000, "input": 5000000, "output": 22500000, "cached": 500000}],
			"tiers": {
				"flex": {"input": 1250000, "output": 7500000, "cached": 130000,
					"long": [{"above": 272000, "input": 2500000, "output": 11250000, "cached": 250000}]},
				"priority": {"input": 5000000, "output": 30000000, "cached": 500000}
			}},
		"voice": {"mode": "realtime", "per_minute": 50000, "per_text_input": 4000},
		"embed": {"mode": "embedding", "input": 100000}
	},
	"presets": {
		"cheap": {"flat": {"mode": "chat", "max_output": 500, "input": 1000000, "output": 4000000}}
	}
}`

func table(t *testing.T) *pricing.Table {
	t.Helper()
	tbl, err := pricing.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func TestEstimate(t *testing.T) {
	tests := []struct {
		name string
		req  pricing.Request
		want int64 // microdollars, worked by hand
	}{
		{"bytes over four at the input price, plus max output at the output price",
			pricing.Request{Model: "flat", RequestBytes: 4000},
			1000*2 + 1000*8}, // 1,000 in at $2/M = 2,000µ; 1,000 out at $8/M = 8,000µ
		{"max_tokens below the max output sets the allowance",
			pricing.Request{Model: "flat", RequestBytes: 4000, MaxTokens: 100},
			2000 + 800},
		{"max_tokens above the max output is capped at it",
			pricing.Request{Model: "flat", RequestBytes: 4000, MaxTokens: 50000},
			2000 + 8000},
		{"the preset's entry wins over the global one",
			pricing.Request{Preset: "cheap", Model: "flat", RequestBytes: 4000},
			1000*1 + 500*4},
		{"a preset without the model falls back to the global entry",
			pricing.Request{Preset: "cheap", Model: "modal", RequestBytes: 0, MaxTokens: 1000},
			24000},
		{"a partial token of input counts as a token",
			pricing.Request{Model: "flat", RequestBytes: 1, MaxTokens: 1},
			2 + 8}, // 1 in at $2/M = 2µ; 1 out at $8/M = 8µ
		{"a request at the threshold stays in the base band",
			pricing.Request{Model: "long", RequestBytes: 272000 * 4, MaxTokens: 1000, ServiceTier: "default"},
			680000 + 15000}, // 272,000 × $2.50/M, 1,000 × $15/M
		{"a request over the threshold is priced whole in the long-context band",
			pricing.Request{Model: "long", RequestBytes: 272001 * 4, MaxTokens: 1000, ServiceTier: "default"},
			1360005 + 22500}, // 272,001 × $5/M, 1,000 × $22.50/M
		{"a requested tier is used",
			pricing.Request{Model: "long", RequestBytes: 4000, MaxTokens: 1000, ServiceTier: "flex"},
			1250 + 7500},
		{"a requested tier has its own long-context band",
			pricing.Request{Model: "long", RequestBytes: 272001 * 4, MaxTokens: 1000, ServiceTier: "flex"},
			680003 + 11250}, // 272,001 × $2.50/M rounds up
		{"no tier given reserves at the highest known",
			pricing.Request{Model: "long", RequestBytes: 4000, MaxTokens: 1000},
			5000 + 30000}, // priority
		{"an unknown tier reserves at the highest known",
			pricing.Request{Model: "long", RequestBytes: 4000, MaxTokens: 1000, ServiceTier: "warp"},
			5000 + 30000},
		{"standard names the base schedule",
			pricing.Request{Model: "long", RequestBytes: 4000, MaxTokens: 1000, ServiceTier: "standard"},
			2500 + 15000},
		{"a voice model reserves its duration per second",
			pricing.Request{Model: "voice", Duration: 90 * time.Second},
			75000},
		{"an embedding has no output allowance",
			pricing.Request{Model: "embed", RequestBytes: 4000},
			100},
	}
	tbl := table(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tbl.Estimate(tt.req)
			if !ok {
				t.Fatalf("Estimate(%+v): unknown model", tt.req)
			}
			if got.Micro != tt.want {
				t.Errorf("Estimate(%+v) = %d µ$, want %d", tt.req, got.Micro, tt.want)
			}
		})
	}
}

func TestEstimateRoundsPartialTokensUp(t *testing.T) {
	got, ok := table(t).Estimate(pricing.Request{Model: "embed", RequestBytes: 1})
	// One byte is one started token: $0.10/M × 1 = 0.1 µ$, rounded up to 1 µ$.
	if !ok || got.Micro != 1 || got.InputTokens != 1 {
		t.Errorf("Estimate = %+v, %v; want 1 token, 1 µ$", got, ok)
	}
}

func TestEstimateSaysWhatItAssumed(t *testing.T) {
	got, ok := table(t).Estimate(pricing.Request{Model: "long", RequestBytes: 4000})
	if !ok {
		t.Fatal("unknown model")
	}
	if got.InputTokens != 1000 || got.OutputTokens != 128000 || got.ServiceTier != "priority" {
		t.Errorf("Estimate = %+v; want 1,000 in, 128,000 out, priority", got)
	}
}

func TestModelIDsMatchExactly(t *testing.T) {
	tbl := table(t)
	for _, model := range []string{"flat-2026-01-01", "FLAT", "fla", "flat ", "openai/flat", ""} {
		if got, ok := tbl.Estimate(pricing.Request{Model: model, RequestBytes: 100}); ok {
			t.Errorf("Estimate(%q) = %+v; want an unknown model", model, got)
		}
		if got, ok := tbl.Cost(pricing.Usage{Model: model, InputTokens: 100}); ok {
			t.Errorf("Cost(%q) = %+v; want an unknown model", model, got)
		}
	}
}

func TestCost(t *testing.T) {
	tests := []struct {
		name string
		u    pricing.Usage
		want int64 // microdollars, worked by hand
	}{
		{"input and output",
			pricing.Usage{Model: "flat", InputTokens: 1000, OutputTokens: 500},
			2000 + 4000},
		{"cached tokens bill at the cached price",
			pricing.Usage{Model: "flat", CachedTokens: 1000},
			500},
		{"cache writes with no price bill at the input price",
			pricing.Usage{Model: "flat", CacheWriteTokens: 1000, CacheWrite1hTokens: 1000},
			2000 + 2000},
		{"audio with no price bills at the text price",
			pricing.Usage{Model: "flat", AudioInputTokens: 1000, AudioOutputTokens: 1000, AudioCachedTokens: 1000},
			2000 + 8000 + 2000},
		{"each modality has its own price",
			pricing.Usage{Model: "modal", InputTokens: 1000, OutputTokens: 1000, CachedTokens: 1000,
				CacheWriteTokens: 1000, CacheWrite1hTokens: 1000,
				AudioInputTokens: 1000, AudioOutputTokens: 1000, AudioCachedTokens: 1000},
			4000 + 24000 + 400 + 5000 + 8000 + 32000 + 64000 + 1000},
		{"settles by the actual input, not the estimate",
			pricing.Usage{Model: "long", InputTokens: 272001, OutputTokens: 1000},
			1360005 + 22500},
		{"every kind of input counts towards the long-context threshold",
			pricing.Usage{Model: "long", InputTokens: 1, CachedTokens: 272000},
			5 + 136000}, // 1 × $5/M, 272,000 × $0.50/M
		{"the reported tier is the one charged",
			pricing.Usage{Model: "long", ServiceTier: "flex", InputTokens: 1000, OutputTokens: 1000},
			1250 + 7500},
		{"no reported tier charges the base schedule",
			pricing.Usage{Model: "long", InputTokens: 1000, OutputTokens: 1000},
			2500 + 15000},
		{"an unknown reported tier charges the highest known",
			pricing.Usage{Model: "long", ServiceTier: "warp", InputTokens: 1000, OutputTokens: 1000},
			5000 + 30000},
		{"server-tool calls add their fee per call",
			pricing.Usage{Model: "modal", Tools: []pricing.ToolCalls{{Name: "web_search", Calls: 3}, {Name: "unpriced", Calls: 9}}},
			30000},
		{"voice bills per second",
			pricing.Usage{Model: "voice", Duration: 90 * time.Second},
			75000},
		{"voice bills part of a second, rounded up",
			pricing.Usage{Model: "voice", Duration: time.Millisecond},
			1},
		{"voice text inputs add their fee",
			pricing.Usage{Model: "voice", TextInputs: 2},
			8000},
		{"the preset's entry is the one charged",
			pricing.Usage{Preset: "cheap", Model: "flat", InputTokens: 1000},
			1000},
		{"the reported model wins over the requested one",
			pricing.Usage{Model: "flat", RequestedModel: "long", InputTokens: 1000},
			2000},
		{"an unknown reported model falls back to the requested one",
			pricing.Usage{Model: "rerouted-9", RequestedModel: "flat", InputTokens: 1000},
			2000},
		{"a provider-reported cost is final",
			pricing.Usage{Model: "flat", InputTokens: 1000, ReportedCost: true, ReportedMicro: 7},
			7},
		{"a provider-reported cost needs no price",
			pricing.Usage{Model: "nowhere", InputTokens: 1000, ReportedCost: true, ReportedMicro: 7},
			7},
		{"a provider-reported cost of zero is final",
			pricing.Usage{Model: "flat", InputTokens: 1000, ReportedCost: true},
			0},
	}
	tbl := table(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tbl.Cost(tt.u)
			if !ok {
				t.Fatalf("Cost(%+v): unknown model", tt.u)
			}
			if got.Micro != tt.want {
				t.Errorf("Cost(%+v) = %d µ$, want %d", tt.u, got.Micro, tt.want)
			}
		})
	}
}

func TestCostNamesWhatItCharged(t *testing.T) {
	tbl := table(t)
	got, _ := tbl.Cost(pricing.Usage{Model: "rerouted-9", RequestedModel: "long", ServiceTier: "warp", InputTokens: 1})
	if got.Model != "long" || got.ServiceTier != "priority" || got.Reported {
		t.Errorf("fell back: %+v; want model long, tier priority", got)
	}
	got, _ = tbl.Cost(pricing.Usage{Model: "flat", ReportedCost: true, ReportedMicro: 7})
	if !got.Reported {
		t.Errorf("reported: %+v; want Reported", got)
	}
}

func TestHugeNumbersSaturateInsteadOfWrapping(t *testing.T) {
	tbl := table(t)
	const huge = int64(1) << 62
	got, ok := tbl.Cost(pricing.Usage{Model: "flat", InputTokens: huge, OutputTokens: huge})
	if !ok || got.Micro <= 0 {
		t.Errorf("Cost of 2^62 tokens = %+v; want a large positive cost", got)
	}
	est, ok := tbl.Estimate(pricing.Request{Model: "flat", RequestBytes: huge})
	if !ok || est.Micro <= 0 {
		t.Errorf("Estimate of 2^62 bytes = %+v; want a large positive reservation", est)
	}
}

func TestParseRefusesATableThatCannotBeEnforced(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"not JSON", `{`, "price table"},
		{"another schema", `{"schema": 2, "generated_at": "2026-11-01T00:00:00Z"}`, "schema"},
		{"no generated_at", `{"schema": 1}`, "generated_at"},
		{"no max output", `{"schema": 1, "generated_at": "2026-11-01T00:00:00Z", "global": {"m": {"mode": "chat", "input": 1, "output": 1}}}`, "max output"},
		{"a negative price", `{"schema": 1, "generated_at": "2026-11-01T00:00:00Z", "global": {"m": {"mode": "chat", "max_output": 1, "input": -1, "output": 1}}}`, "negative"},
		{"a negative optional price", `{"schema": 1, "generated_at": "2026-11-01T00:00:00Z", "presets": {"p": {"m": {"mode": "chat", "max_output": 1, "cached": -1}}}}`, "negative"},
		{"bands out of order", `{"schema": 1, "generated_at": "2026-11-01T00:00:00Z", "global": {"m": {"mode": "chat", "max_output": 1, "long": [{"above": 9}, {"above": 2}]}}}`, "ascending"},
		{"a band in a tier out of order", `{"schema": 1, "generated_at": "2026-11-01T00:00:00Z", "global": {"m": {"mode": "chat", "max_output": 1, "tiers": {"flex": {"long": [{"above": 9}, {"above": 9}]}}}}}`, "ascending"},
		{"an unknown field", `{"schema": 1, "generated_at": "2026-11-01T00:00:00Z", "global": {"m": {"mode": "chat", "max_output": 1, "inptu": 1}}}`, "inptu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pricing.Parse([]byte(tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse = %v, want an error about %q", err, tt.want)
			}
		})
	}
}

func TestAgeNeverChangesEnforcement(t *testing.T) {
	fresh := table(t)
	ancient, err := pricing.Parse([]byte(strings.Replace(sample, "2026-11-01", "2001-01-01", 1)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	if fresh.Stale(now) || !ancient.Stale(now) {
		t.Fatalf("Stale: fresh %v, ancient %v", fresh.Stale(now), ancient.Stale(now))
	}
	req := pricing.Request{Model: "long", RequestBytes: 4000}
	use := pricing.Usage{Model: "long", InputTokens: 1000, OutputTokens: 1000}
	e1, ok1 := fresh.Estimate(req)
	e2, ok2 := ancient.Estimate(req)
	if e1 != e2 || ok1 != ok2 {
		t.Errorf("Estimate: fresh %+v, ancient %+v", e1, e2)
	}
	c1, ok1 := fresh.Cost(use)
	c2, ok2 := ancient.Cost(use)
	if c1 != c2 || ok1 != ok2 {
		t.Errorf("Cost: fresh %+v, ancient %+v", c1, c2)
	}
}

func TestAgeAndStaleness(t *testing.T) {
	tbl := table(t) // generated 2026-11-01
	at := func(days int) time.Time { return tbl.GeneratedAt().AddDate(0, 0, days) }
	if got := tbl.Age(at(3)); got != 72*time.Hour {
		t.Errorf("Age after 3 days = %v", got)
	}
	if tbl.Stale(at(60)) {
		t.Error("stale at exactly 60 days; the upgrade note starts past 60")
	}
	if !tbl.Stale(at(60).Add(time.Second)) {
		t.Error("not stale past 60 days")
	}
	if got := tbl.Age(at(-1)); got != 0 {
		t.Errorf("Age with a clock behind the table = %v, want 0", got)
	}
}

func TestEstimateAndCostDoNotAllocate(t *testing.T) {
	tbl := table(t)
	req := pricing.Request{Preset: "cheap", Model: "long", RequestBytes: 4000}
	use := pricing.Usage{Preset: "cheap", Model: "long", ServiceTier: "flex", InputTokens: 1000, OutputTokens: 1000}
	if n := testing.AllocsPerRun(100, func() { tbl.Estimate(req) }); n != 0 {
		t.Errorf("Estimate allocates %v times", n)
	}
	if n := testing.AllocsPerRun(100, func() { tbl.Cost(use) }); n != 0 {
		t.Errorf("Cost allocates %v times", n)
	}
}

func BenchmarkEstimate(b *testing.B) {
	tbl, err := pricing.Parse([]byte(sample))
	if err != nil {
		b.Fatal(err)
	}
	req := pricing.Request{Model: "long", RequestBytes: 4000}
	b.ReportAllocs()
	for b.Loop() {
		tbl.Estimate(req)
	}
}

func BenchmarkCost(b *testing.B) {
	tbl, err := pricing.Parse([]byte(sample))
	if err != nil {
		b.Fatal(err)
	}
	use := pricing.Usage{Model: "long", ServiceTier: "flex", InputTokens: 1000, OutputTokens: 1000}
	b.ReportAllocs()
	for b.Loop() {
		tbl.Cost(use)
	}
}

// overridden is the sample table with an install override of "flat" and of a
// new model, and an override of "flat" on upstream up_a only.
//
//	install: flat $3 in, $9 out, 2,000 max output; fresh $5 in, $10 out
//	up_a: flat $6 in, $12 out, 3,000 max output
func overridden(t *testing.T) *pricing.Table {
	t.Helper()
	tbl, err := table(t).WithOverrides([]pricing.Override{
		{Model: "flat", MaxOutput: 2000, Rates: pricing.Rates{Input: 3000000, Output: 9000000}},
		{Model: "fresh", MaxOutput: 1000, Rates: pricing.Rates{Input: 5000000, Output: 10000000}},
		{UpstreamID: "up_a", Model: "flat", MaxOutput: 3000, Rates: pricing.Rates{Input: 6000000, Output: 12000000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func TestLookupOrder(t *testing.T) {
	// The built-in layers alone: "flat" is global and on the cheap preset.
	plain, err := table(t).WithOverrides([]pricing.Override{
		{UpstreamID: "up_a", Model: "only-on-a", MaxOutput: 1, Rates: pricing.Rates{Input: 7}},
		{Model: "only-install", MaxOutput: 1, Rates: pricing.Rates{Input: 8}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name             string
		tbl              *pricing.Table
		upstream, preset string
		model            string
		origin           pricing.Origin
		input            int64
	}{
		{"the per-upstream override beats everything", overridden(t), "up_a", "cheap", "flat", pricing.OriginUpstreamOverride, 6000000},
		{"the install override beats the preset's entry", overridden(t), "up_b", "cheap", "flat", pricing.OriginInstallOverride, 3000000},
		{"the install override beats the global entry", overridden(t), "up_b", "", "flat", pricing.OriginInstallOverride, 3000000},
		{"another upstream's override doesn't apply", plain, "up_b", "", "only-on-a", 0, 0},
		{"an install override prices a model the table lacks", overridden(t), "up_b", "", "fresh", pricing.OriginInstallOverride, 5000000},
		{"an install override reaches an upstream with no id", plain, "", "", "only-install", pricing.OriginInstallOverride, 8},
		{"the preset's entry beats the global entry", plain, "up_b", "cheap", "flat", pricing.OriginPreset, 1000000},
		{"the global entry is last", plain, "up_b", "cheap", "modal", pricing.OriginGlobal, 4000000},
		{"a custom upstream sees the global entry", plain, "up_b", "", "flat", pricing.OriginGlobal, 2000000},
		{"ids match exactly", overridden(t), "up_a", "", "FLAT", 0, 0},
		{"nothing knows it", overridden(t), "up_a", "cheap", "nope", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, origin, ok := tt.tbl.Resolve(tt.upstream, tt.preset, tt.model)
			if want := tt.origin != 0; ok != want {
				t.Fatalf("ok = %v, want %v", ok, want)
			}
			if !ok {
				return
			}
			if origin != tt.origin || e.Input != tt.input {
				t.Errorf("origin %v, input %d; want %v, %d", origin, e.Input, tt.origin, tt.input)
			}
		})
	}
}

func TestEstimateAndCostUseTheOverride(t *testing.T) {
	tbl := overridden(t)
	est, ok := tbl.Estimate(pricing.Request{UpstreamID: "up_a", Preset: "cheap", Model: "flat", RequestBytes: 4000})
	// 1,000 in at $6/M = 6,000µ; the override's 3,000 max output at $12/M = 36,000µ.
	if !ok || est.Micro != 42000 || est.OutputTokens != 3000 {
		t.Errorf("estimate = %+v, %v", est, ok)
	}
	cost, ok := tbl.Cost(pricing.Usage{UpstreamID: "up_b", Model: "fresh", InputTokens: 1000, OutputTokens: 1000})
	// 1,000 in at $5/M = 5,000µ; 1,000 out at $10/M = 10,000µ.
	if !ok || cost.Micro != 15000 || cost.Model != "fresh" {
		t.Errorf("cost = %+v, %v", cost, ok)
	}
}

func TestAnOverrideReplacesTheWholeEntry(t *testing.T) {
	tbl, err := table(t).WithOverrides([]pricing.Override{{Model: "long", MaxOutput: 1000, Rates: pricing.Rates{Input: 1000000, Output: 2000000}}})
	if err != nil {
		t.Fatal(err)
	}
	// Over the built-in's 272k band and asking for its priority tier, neither
	// of which the override has: 300,000 in at $1/M, 1,000 out at $2/M.
	cost, ok := tbl.Cost(pricing.Usage{Model: "long", ServiceTier: "priority", InputTokens: 300000, OutputTokens: 1000})
	if !ok || cost.Micro != 302000 || cost.ServiceTier != "" {
		t.Errorf("cost = %+v, %v", cost, ok)
	}
}

func TestOverrideOptionalPricesFallBackLikeTheTable(t *testing.T) {
	cached := int64(1000000)
	tbl, err := table(t).WithOverrides([]pricing.Override{{Model: "new", MaxOutput: 1000, Rates: pricing.Rates{Input: 4000000, Output: 8000000, Cached: &cached}}})
	if err != nil {
		t.Fatal(err)
	}
	// 1,000 cached at $1/M = 1,000µ; 1,000 cache writes at the input's $4/M = 4,000µ.
	cost, ok := tbl.Cost(pricing.Usage{Model: "new", CachedTokens: 1000, CacheWriteTokens: 1000})
	if !ok || cost.Micro != 5000 {
		t.Errorf("cost = %+v, %v", cost, ok)
	}
}

func TestPriceMultiplier(t *testing.T) {
	tbl := overridden(t)
	const uplift = 1_100_000 // ×1.1
	tests := []struct {
		name string
		req  pricing.Request
		want int64
	}{
		{"applies to a global entry", pricing.Request{Model: "modal", MaxTokens: 1000, MultiplierPPM: uplift}, 26400},      // 1,000 out at $24/M = 24,000µ, ×1.1
		{"applies to an install override", pricing.Request{Model: "fresh", MaxTokens: 1000, MultiplierPPM: uplift}, 11000}, // 1,000 out at $10/M = 10,000µ, ×1.1
		{"doesn't apply to a per-upstream override", pricing.Request{UpstreamID: "up_a", Model: "flat", MaxTokens: 1000, MultiplierPPM: uplift}, 12000},
		{"zero means ×1", pricing.Request{Model: "fresh", MaxTokens: 1000}, 10000},
		{"exactly ×1 changes nothing", pricing.Request{Model: "fresh", MaxTokens: 1000, MultiplierPPM: 1_000_000}, 10000},
		{"a discount rounds up", pricing.Request{Model: "fresh", MaxTokens: 1, MultiplierPPM: 333_333}, 4}, // 10µ × 0.333333 = 3.33333µ
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tbl.Estimate(tt.req)
			if !ok || got.Micro != tt.want {
				t.Errorf("estimate = %+v, %v; want %d", got, ok, tt.want)
			}
		})
	}
}

func TestPriceMultiplierAppliesToAPresetsEntry(t *testing.T) {
	// 500 out, the most it allows, at the cheap preset's $4/M = 2,000µ, ×1.1.
	got, ok := table(t).Estimate(pricing.Request{Preset: "cheap", Model: "flat", MaxTokens: 500, MultiplierPPM: 1_100_000})
	if !ok || got.Micro != 2200 {
		t.Errorf("estimate = %+v, %v", got, ok)
	}
}

func TestPriceMultiplierOnCost(t *testing.T) {
	tbl := overridden(t)
	const uplift = 1_500_000 // ×1.5
	tests := []struct {
		name string
		u    pricing.Usage
		want int64
	}{
		{"a built-in price is multiplied", pricing.Usage{Model: "modal", OutputTokens: 1000, MultiplierPPM: uplift}, 36000},
		{"an install override is multiplied", pricing.Usage{Model: "fresh", OutputTokens: 1000, MultiplierPPM: uplift}, 15000},
		{"a per-upstream override isn't", pricing.Usage{UpstreamID: "up_a", Model: "flat", OutputTokens: 1000, MultiplierPPM: uplift}, 12000},
		{"a provider-reported cost isn't", pricing.Usage{Model: "modal", OutputTokens: 1000, MultiplierPPM: uplift, ReportedCost: true, ReportedMicro: 777}, 777},
		{"voice minutes are prices too", pricing.Usage{Model: "voice", Duration: time.Minute, MultiplierPPM: uplift}, 75000},
		{"tool fees are prices too", pricing.Usage{Model: "modal", Tools: []pricing.ToolCalls{{Name: "web_search", Calls: 1}}, MultiplierPPM: uplift}, 15000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tbl.Cost(tt.u)
			if !ok || got.Micro != tt.want {
				t.Errorf("cost = %+v, %v; want %d", got, ok, tt.want)
			}
		})
	}
}

func TestPriceMultiplierDoesNotWrapAtTheTop(t *testing.T) {
	tbl := overridden(t)
	got, ok := tbl.Cost(pricing.Usage{Model: "fresh", OutputTokens: math.MaxInt64, MultiplierPPM: 100_000_000})
	if !ok || got.Micro != math.MaxInt64 {
		t.Errorf("cost = %+v, %v", got, ok)
	}
}

func TestWithOverridesRefuses(t *testing.T) {
	good := pricing.Rates{Input: 1, Output: 1}
	negative := int64(-1)
	tests := []struct {
		name      string
		overrides []pricing.Override
		want      string
	}{
		{"no max output", []pricing.Override{{Model: "m", Rates: good}}, "no max output"},
		{"a negative price", []pricing.Override{{Model: "m", MaxOutput: 1, Rates: pricing.Rates{Input: -1}}}, "negative price"},
		{"a negative optional price", []pricing.Override{{Model: "m", MaxOutput: 1, Rates: pricing.Rates{Cached: &negative}}}, "negative price"},
		{"no model", []pricing.Override{{MaxOutput: 1, Rates: good}}, "no model"},
		{"the same model twice", []pricing.Override{{Model: "m", MaxOutput: 1, Rates: good}, {Model: "m", MaxOutput: 1, Rates: good}}, "twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := table(t).WithOverrides(tt.overrides)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestWithOverridesReplacesAndLeavesTheOriginalAlone(t *testing.T) {
	base := table(t)
	once := overridden(t)
	if _, _, ok := base.Resolve("", "", "fresh"); ok {
		t.Error("the original table gained an override")
	}
	again, err := once.WithOverrides(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := again.Resolve("", "", "fresh"); ok {
		t.Error("an empty set kept the old override")
	}
	if _, _, ok := once.Resolve("", "", "fresh"); !ok {
		t.Error("the earlier table lost its override")
	}
	if e, origin, ok := again.Resolve("up_a", "", "flat"); !ok || origin != pricing.OriginGlobal || e.Input != 2000000 {
		t.Errorf("after removal: %+v, %v, %v", e, origin, ok)
	}
}

func TestPricesSwapsOverridesWhole(t *testing.T) {
	prices := pricing.NewPrices(table(t))
	if _, _, ok := prices.Table().Resolve("", "", "fresh"); ok {
		t.Fatal("a new Prices has an override")
	}
	if err := prices.SetOverrides([]pricing.Override{{Model: "fresh", MaxOutput: 1, Rates: pricing.Rates{Input: 1}}}); err != nil {
		t.Fatal(err)
	}
	held := prices.Table()
	if _, _, ok := held.Resolve("", "", "fresh"); !ok {
		t.Error("the override didn't arrive")
	}
	if err := prices.SetOverrides([]pricing.Override{{MaxOutput: 1}}); err == nil {
		t.Error("a bad set was accepted")
	}
	if _, _, ok := prices.Table().Resolve("", "", "fresh"); !ok {
		t.Error("a refused set changed what's in force")
	}
	if err := prices.SetOverrides(nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := prices.Table().Resolve("", "", "fresh"); ok {
		t.Error("the override wasn't removed")
	}
	if _, _, ok := held.Resolve("", "", "fresh"); !ok {
		t.Error("a table a request holds changed under it")
	}
}

func TestOverriddenLookupsAllocateNothing(t *testing.T) {
	tbl := overridden(t)
	req := pricing.Request{UpstreamID: "up_a", Preset: "cheap", Model: "flat", RequestBytes: 4000, MultiplierPPM: 1_100_000}
	u := pricing.Usage{UpstreamID: "up_b", Model: "fresh", InputTokens: 1, OutputTokens: 1, MultiplierPPM: 1_100_000}
	if n := testing.AllocsPerRun(100, func() {
		tbl.Estimate(req)
		tbl.Cost(u)
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}
