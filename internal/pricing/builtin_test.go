package pricing_test

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"context"

	"github.com/kirank55/kman/internal/pricing"
)

// These prices were read from the providers' pages (docs/research on the
// research/model-price-sources branch, and rechecked 2026-11-06). They are not
// derived from the table: a weekly data pull that moves one of them should make
// a person look, and then update it here.
func TestTheEmbeddedTableAgreesWithTheProviderPages(t *testing.T) {
	tests := []struct {
		name          string
		preset, model string
		usage         pricing.Usage
		want          int64
	}{
		{"gpt-5.4: $2.50 in, $15 out", "openai", "gpt-5.4",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000}, 2500 + 15000},
		{"gpt-5.4: $0.25 cached", "openai", "gpt-5.4", pricing.Usage{CachedTokens: 1000}, 250},
		{"gpt-5.4 at 272K stays in the base band", "openai", "gpt-5.4",
			pricing.Usage{InputTokens: 272000}, 680000},
		{"gpt-5.4 over 272K: $5 in, $22.50 out", "openai", "gpt-5.4",
			pricing.Usage{InputTokens: 272001, OutputTokens: 1000}, 1360005 + 22500},
		{"a dated id has its own price: gpt-4o-2024-05-13 at $5/$15", "openai", "gpt-4o-2024-05-13",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000}, 5000 + 15000},
		{"gpt-4o at $2.50/$10", "openai", "gpt-4o",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000}, 2500 + 10000},
		{"gpt-realtime-2.1 text $4/$24, audio $32/$64", "openai", "gpt-realtime-2.1",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000, AudioInputTokens: 1000, AudioOutputTokens: 1000}, 4000 + 24000 + 32000 + 64000},
		{"gpt-live-1: $0.05 per minute, billed per second", "openai", "gpt-live-1",
			pricing.Usage{Duration: 90 * time.Second}, 75000},
		{"claude-opus-5-5: $4 in, $20 out", "anthropic", "claude-opus-5-5",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000}, 4000 + 20000},
		{"claude-opus-5-5: $0.20 hit, $5 write, $8 one-hour write", "anthropic", "claude-opus-5-5",
			pricing.Usage{CachedTokens: 1000, CacheWriteTokens: 1000, CacheWrite1hTokens: 1000}, 200 + 5000 + 8000},
		{"claude-opus-5-5 fast: $8 in, $40 out", "anthropic", "claude-opus-5-5",
			pricing.Usage{ServiceTier: "fast", InputTokens: 1000, OutputTokens: 1000}, 8000 + 40000},
		{"claude-opus-5-5: $10 per 1,000 web searches", "anthropic", "claude-opus-5-5",
			pricing.Usage{Tools: []pricing.ToolCalls{{Name: "web_search", Calls: 3}}}, 30000},
		{"claude-haiku-5-5 over 100K: $0.50 in, $2.50 out", "anthropic", "claude-haiku-5-5",
			pricing.Usage{InputTokens: 100001, OutputTokens: 1000}, 50001 + 2500},
		{"gemini-2.5-pro over 200K: $2.50 in, $15 out", "gemini", "gemini-2.5-pro",
			pricing.Usage{InputTokens: 200001, OutputTokens: 1000}, 500003 + 15000},
		{"deepseek-v4-pro at the peak $1.32/$3.96", "deepseek", "deepseek-v4-pro",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000}, 1320 + 3960},
		{"deepseek-v4-pro: a $0.044 hit at peak", "deepseek", "deepseek-v4-pro",
			pricing.Usage{CachedTokens: 1000}, 44},
		{"grok-4.7 at or above 200K: $4 in, $12 out", "xai", "grok-4.7",
			pricing.Usage{InputTokens: 200000, OutputTokens: 1000}, 800000 + 12000},
		{"grok-voice-3.0: $0.08 per minute, $0.004 per text input", "xai", "grok-voice-3.0",
			pricing.Usage{Duration: time.Minute, TextInputs: 1}, 80000 + 4000},
		{"groq's gpt-oss-120b: $0.15/$0.60", "groq", "openai/gpt-oss-120b",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000}, 150 + 600},
		{"fireworks' kimi-k3: $3/$15", "fireworks", "accounts/fireworks/models/kimi-k3",
			pricing.Usage{InputTokens: 1000, OutputTokens: 1000}, 3000 + 15000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := tt.usage
			u.Preset, u.Model = tt.preset, tt.model
			got, ok := pricing.Builtin().Cost(u)
			if !ok {
				t.Fatalf("%s/%s is not in the table", tt.preset, tt.model)
			}
			if got.Micro != tt.want {
				t.Errorf("Cost = %d µ$, want %d", got.Micro, tt.want)
			}
		})
	}
}

func TestTheEmbeddedMaxOutputsAgreeWithTheProviderPages(t *testing.T) {
	for _, tt := range []struct {
		preset, model string
		want          int64
	}{
		{"openai", "gpt-5.4", 128000},
		{"openai", "gpt-4.1", 32768}, // LiteLLM's context window would reserve about $8
		{"anthropic", "claude-opus-5-5", 128000},
		{"gemini", "gemini-2.5-pro", 65536},
		{"groq", "openai/gpt-oss-120b", 65536},
		{"cerebras", "gpt-oss-120b", 40960}, // the paid tier; LiteLLM has the free tier's 32,768
		{"deepseek", "deepseek-v4-pro", 393216},
	} {
		e, ok := pricing.Builtin().Lookup(tt.preset, tt.model)
		if !ok {
			t.Errorf("%s/%s is not in the table", tt.preset, tt.model)
			continue
		}
		if e.MaxOutput != tt.want || !e.Reviewed {
			t.Errorf("%s/%s: max output %d (reviewed: %v), want %d, reviewed", tt.preset, tt.model, e.MaxOutput, e.Reviewed, tt.want)
		}
	}
}

func TestTheEmbeddedTableHasEveryLLMPreset(t *testing.T) {
	counts := map[string]int{}
	for row := range pricing.Builtin().All() {
		counts[row.Preset]++
	}
	// openai and anthropic are the global section ("").
	for _, section := range []string{"", "cerebras", "deepseek", "fireworks", "gemini", "groq", "mistral", "openrouter", "together", "xai"} {
		if counts[section] == 0 {
			t.Errorf("section %q is empty", section)
		}
	}
}

func TestDatedIDsAndAliasesAreInTheEmbeddedTable(t *testing.T) {
	for _, tt := range [][2]string{
		{"openai", "gpt-5.4-2026-03-05"},
		{"openai", "gpt-5-4"},
		{"openai", "gpt-4o-2024-05-13"},
		{"anthropic", "claude-haiku-4-5-20251001"},
	} {
		if _, ok := pricing.Builtin().Lookup(tt[0], tt[1]); !ok {
			t.Errorf("%s/%s is not in the table", tt[0], tt[1])
		}
	}
	if _, ok := pricing.Builtin().Lookup("openai", "gpt-5.4-unknown"); ok {
		t.Error("an id no source lists matched")
	}
}

func TestTheEmbeddedTableIsDated(t *testing.T) {
	if pricing.Builtin().GeneratedAt().IsZero() {
		t.Error("no generated_at")
	}
}

// failingTransport fails the test on any request through the default client.
type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("price data asked the network for %s", r.URL)
	return nil, http.ErrUseLastResponse
}

// TestPriceDataUsesNoNetwork answers a price from a fresh parse with the default
// client and resolver failing the test. The gate
// TestPricingCannotReachTheNetwork covers what this can't: a dial of its own.
func TestPriceDataUsesNoNetwork(t *testing.T) {
	transport, resolver := http.DefaultTransport, net.DefaultResolver
	t.Cleanup(func() { http.DefaultTransport, net.DefaultResolver = transport, resolver })
	http.DefaultTransport = failingTransport{t}
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		t.Error("price data resolved a name")
		return nil, net.ErrClosed
	}}

	raw, err := json.Marshal(pricing.Data{Schema: pricing.Schema, GeneratedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pricing.Parse(raw); err != nil {
		t.Fatal(err)
	}
	tbl := pricing.Builtin()
	tbl.Estimate(pricing.Request{Preset: "openai", Model: "gpt-5.4", RequestBytes: 400})
	tbl.Cost(pricing.Usage{Preset: "openai", Model: "gpt-5.4", InputTokens: 100})
	tbl.Stale(time.Unix(2, 0))
}
