package pricing_test

import (
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
