package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/pricing"
)

var now = time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)

func p(v int64) *int64 { return &v }

// sources are three small datasets in their real shapes. Prices below are as
// each dataset states them; expected values in the tests are worked by hand.
type sources struct{ litellm, genai, modelsdev, reviewed string }

func (s sources) inputs() inputs {
	return inputs{
		litellm:   []byte(or(s.litellm, `{"gpt-x": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06, "max_output_tokens": 10}}`)),
		genai:     []byte(or(s.genai, `[{"id": "openai", "models": [{"id": "gpt-x", "prices": {"input_mtok": 1, "output_mtok": 2}}]}]`)),
		modelsdev: []byte(or(s.modelsdev, `{"openai": {"models": {"gpt-x": {"cost": {"input": 1, "output": 2}, "limit": {"output": 10}}}}}`)),
		reviewed:  []byte(or(s.reviewed, `{}`)),
	}
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func gen(t *testing.T, s sources) (pricing.Data, *report) {
	t.Helper()
	d, r, err := generate(s.inputs(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pricing.New(d); err != nil {
		t.Fatalf("the generated table doesn't parse: %v", err)
	}
	return d, r
}

func TestLiteLLMRowBecomesAnEntry(t *testing.T) {
	d, _ := gen(t, sources{litellm: `{
		"gpt-5.4": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05, "cache_read_input_token_cost": 2.5e-07,
			"input_cost_per_token_above_272k_tokens": 5e-06, "output_cost_per_token_above_272k_tokens": 2.25e-05, "cache_read_input_token_cost_above_272k_tokens": 5e-07,
			"input_cost_per_token_flex": 1.25e-06, "output_cost_per_token_flex": 7.5e-06,
			"input_cost_per_token_priority": 5e-06, "output_cost_per_token_priority": 3e-05,
			"input_cost_per_token_batches": 1.25e-06, "output_cost_per_token_batches": 7.5e-06,
			"input_cost_per_token_above_272k_tokens_batches": 2.5e-06, "output_cost_per_token_above_272k_tokens_batches": 1.125e-05,
			"max_input_tokens": 1050000, "max_output_tokens": 128000,
			"search_context_cost_per_query": {"search_context_size_low": 0.01, "search_context_size_high": 0.03}}
	}`, genai: `[]`, modelsdev: `{}`})
	want := pricing.Entry{
		Mode: "chat", ContextWindow: 1050000, MaxOutput: 128000,
		Schedule: pricing.Schedule{
			Rates: pricing.Rates{Input: 2500000, Output: 15000000, Cached: p(250000)},
			Long:  []pricing.Band{{Above: 272000, Rates: pricing.Rates{Input: 5000000, Output: 22500000, Cached: p(500000)}}},
		},
		Tiers: map[string]*pricing.Schedule{
			"flex":     {Rates: pricing.Rates{Input: 1250000, Output: 7500000, Cached: p(250000)}, Long: []pricing.Band{{Above: 272000, Rates: pricing.Rates{Input: 1250000, Output: 7500000, Cached: p(500000)}}}},
			"priority": {Rates: pricing.Rates{Input: 5000000, Output: 30000000, Cached: p(250000)}, Long: []pricing.Band{{Above: 272000, Rates: pricing.Rates{Input: 5000000, Output: 30000000, Cached: p(500000)}}}},
			"batch":    {Rates: pricing.Rates{Input: 1250000, Output: 7500000, Cached: p(250000)}, Long: []pricing.Band{{Above: 272000, Rates: pricing.Rates{Input: 2500000, Output: 11250000, Cached: p(500000)}}}},
		},
		ToolFees: map[string]int64{"web_search": 30000},
	}
	if got := d.Global["gpt-5.4"]; !reflect.DeepEqual(got, want) {
		t.Errorf("entry =\n%s\nwant\n%s", show(got), show(want))
	}
}

func TestModalityPricesComeThrough(t *testing.T) {
	d, _ := gen(t, sources{litellm: `{
		"gpt-realtime-2.1": {"litellm_provider": "openai", "mode": "realtime", "max_output_tokens": 32000,
			"input_cost_per_token": 4e-06, "output_cost_per_token": 2.4e-05, "cache_read_input_token_cost": 4e-07,
			"input_cost_per_audio_token": 3.2e-05, "output_cost_per_audio_token": 6.4e-05, "cache_read_input_audio_token_cost": 4e-07},
		"claude-opus-5-5": {"litellm_provider": "anthropic", "mode": "chat", "max_output_tokens": 128000,
			"input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05, "cache_read_input_token_cost": 2e-07,
			"cache_creation_input_token_cost": 5e-06, "cache_creation_input_token_cost_above_1hr": 8e-06}
	}`, genai: `[]`, modelsdev: `{}`})
	rt := d.Global["gpt-realtime-2.1"]
	if rt.Mode != "realtime" || rt.Input != 4000000 || *rt.AudioInput != 32000000 || *rt.AudioOutput != 64000000 || *rt.AudioCached != 400000 {
		t.Errorf("realtime entry = %s", show(rt))
	}
	op := d.Global["claude-opus-5-5"]
	if *op.CacheWrite != 5000000 || *op.CacheWrite1h != 8000000 || *op.Cached != 200000 {
		t.Errorf("cache entry = %s", show(op))
	}
}

func TestPerMinuteVoiceAndTextInputFees(t *testing.T) {
	d, _ := gen(t, sources{litellm: `{
		"gpt-live-1": {"litellm_provider": "openai", "mode": "realtime", "input_cost_per_second": 0.000833333333333},
		"xai/grok-voice-3.0": {"litellm_provider": "xai", "mode": "realtime", "input_cost_per_second": 0.001333333333333, "input_cost_per_text_message": 0.004}
	}`, genai: `[{"id": "openai", "models": [{"id": "gpt-live-2", "prices": {"audio_hours": 6}}]},
		{"id": "x-ai", "models": [{"id": "grok-voice-4", "prices": {"audio_hours": 6, "input_text_messages_kcount": 5}}]}]`, modelsdev: `{}`})
	if e := d.Global["gpt-live-1"]; e.PerMinute != 50000 {
		t.Errorf("gpt-live-1 = %s; want $0.05 per minute", show(e))
	}
	if e := d.Presets["xai"]["grok-voice-3.0"]; e.PerMinute != 80000 || e.PerTextInput != 4000 {
		t.Errorf("grok-voice-3.0 = %s; want $0.08 per minute and $0.004 per text input", show(e))
	}
	if e := d.Global["gpt-live-2"]; e.PerMinute != 100000 || e.Mode != "realtime" {
		t.Errorf("gpt-live-2 = %s; want $6/hour as $0.10 per minute", show(e))
	}
	if e := d.Presets["xai"]["grok-voice-4"]; e.PerTextInput != 5000 {
		t.Errorf("grok-voice-4 = %s; want $0.005 per text input", show(e))
	}
}

func TestTheMostConservativeValueIsKept(t *testing.T) {
	d, r := gen(t, sources{
		litellm: `{"groq/llama-9": {"litellm_provider": "groq", "mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 3e-06, "cache_read_input_token_cost": 5e-07, "max_input_tokens": 1000, "max_output_tokens": 100}}`,
		genai:   `[{"id": "groq", "models": [{"id": "llama-9", "context_window": 2000, "prices": {"input_mtok": 2, "output_mtok": 1, "cache_read_mtok": 0.25}}]}]`,
		modelsdev: `{"groq": {"models": {"llama-9": {"cost": {"input": 1, "output": 2, "cache_read": 0.1, "cache_write": 3},
			"limit": {"context": 1500, "output": 200}}}}}`,
	})
	e := d.Presets["groq"]["llama-9"]
	if e.Input != 2000000 || e.Output != 3000000 || *e.Cached != 500000 || *e.CacheWrite != 3000000 {
		t.Errorf("prices = %s; want the higher of each", show(e))
	}
	if e.MaxOutput != 200 || e.ContextWindow != 2000 {
		t.Errorf("limits = %s; want the larger of each", show(e))
	}
	var seen []string
	for _, x := range r.Disagreements {
		seen = append(seen, x.Field)
		if x.Preset != "groq" || x.Model != "llama-9" {
			t.Errorf("disagreement about %+v", x)
		}
	}
	for _, f := range []string{"input", "output", "cached", "max_output"} {
		if !strings.Contains(strings.Join(seen, " "), f) {
			t.Errorf("no disagreement reported about %s; got %v", f, seen)
		}
	}
	if strings.Contains(strings.Join(seen, " "), "cache_write") {
		t.Errorf("one source stating a price isn't a disagreement; got %v", seen)
	}
}

func TestLongContextThresholdsThatDifferKeepTheLowerOne(t *testing.T) {
	// xAI bills "at or above 200K": genai-prices says over 199,999, LiteLLM over 200,000.
	d, _ := gen(t, sources{
		litellm: `{"xai/grok-4.7": {"litellm_provider": "xai", "mode": "chat", "max_output_tokens": 9,
			"input_cost_per_token": 2e-06, "output_cost_per_token": 6e-06,
			"input_cost_per_token_above_200k_tokens": 4e-06, "output_cost_per_token_above_200k_tokens": 1.2e-05}}`,
		genai: `[{"id": "x-ai", "models": [{"id": "grok-4.7", "prices": {
			"input_mtok": {"base": 2, "tiers": [{"start": 199999, "price": 4}]},
			"output_mtok": {"base": 6, "tiers": [{"start": 199999, "price": 12}]}}}]}]`,
		modelsdev: `{"xai": {"models": {"grok-4.7": {"limit": {"output": 9}, "cost": {"input": 2, "output": 6,
			"tiers": [{"input": 4, "output": 12, "tier": {"type": "context", "size": 200000}}]}}}}}`,
	})
	e := d.Presets["xai"]["grok-4.7"]
	want := []pricing.Band{{Above: 199999, Rates: pricing.Rates{Input: 4000000, Output: 12000000}}}
	if !reflect.DeepEqual(e.Long, want) {
		t.Errorf("bands = %s, want one over 199,999", show(e))
	}
}

func TestModelsDevLegacyContextField(t *testing.T) {
	d, _ := gen(t, sources{litellm: `{}`, genai: `[]`, modelsdev: `{"google": {"models": {"gemini-9": {"limit": {"output": 9},
		"cost": {"input": 1, "output": 2, "context_over_200k": {"input": 2, "output": 4}}}}},
		"openai": {"models": {"o9": {"limit": {"output": 9}, "cost": {"input": 1, "output": 2},
			"experimental": {"modes": {"fast": {"cost": {"input": 3, "output": 6}}}}}}}}`})
	e := d.Presets["gemini"]["gemini-9"]
	if len(e.Long) != 1 || e.Long[0].Above != 200000 || e.Long[0].Input != 2000000 {
		t.Errorf("gemini-9 = %s", show(e))
	}
	if f := d.Global["o9"].Tiers["fast"]; f == nil || f.Input != 3000000 {
		t.Errorf("o9 = %s; want a fast tier", show(d.Global["o9"]))
	}
}

func TestModelsDevLegacyContextFieldYieldsToARealThreshold(t *testing.T) {
	// models.dev states gpt-5.4's 272K tier twice: as a tier with its size, and
	// under the old name context_over_200k, which is only a name.
	d, _ := gen(t, sources{litellm: `{}`, genai: `[]`, modelsdev: `{"openai": {"models": {"gpt-5.4": {"limit": {"output": 9},
		"cost": {"input": 2.5, "output": 15, "context_over_200k": {"input": 5, "output": 22.5},
			"tiers": [{"input": 5, "output": 22.5, "tier": {"type": "context", "size": 272000}}]}}}}}`})
	want := []pricing.Band{{Above: 272000, Rates: pricing.Rates{Input: 5000000, Output: 22500000}}}
	if got := d.Global["gpt-5.4"].Long; !reflect.DeepEqual(got, want) {
		t.Errorf("bands = %s, want one over 272,000", show(got))
	}
}

func TestAnAliasTakesTheLimitsOfTheModelItNames(t *testing.T) {
	// Only genai-prices lists the dated id, and it has no limits. It says the
	// two ids are one model, and the others give that model's limits.
	d, r := gen(t, sources{
		litellm: `{"gpt-a": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "max_input_tokens": 50, "max_output_tokens": 5}}`,
		genai: `[{"id": "openai", "models": [
			{"id": "gpt-a", "match": {"or": [{"equals": "gpt-a"}, {"equals": "gpt-a-2026-01-01"}]}, "prices": {"input_mtok": 1, "output_mtok": 2}},
			{"id": "gpt-orphan", "prices": {"input_mtok": 1, "output_mtok": 2}}]}]`,
		modelsdev: `{}`,
	})
	if e := d.Global["gpt-a-2026-01-01"]; e.MaxOutput != 5 || e.ContextWindow != 50 {
		t.Errorf("the dated id = %s; want gpt-a's limits", show(e))
	}
	if _, ok := d.Global["gpt-orphan"]; ok {
		t.Errorf("no source gives gpt-orphan a limit, so it can't be in the table")
	}
	if !strings.Contains(strings.Join(r.Dropped, "\n"), "openai/gpt-orphan: no max output") {
		t.Errorf("dropped = %v", r.Dropped)
	}
}

func TestDeepSeekIsPricedAtPeak(t *testing.T) {
	d, _ := gen(t, sources{
		// LiteLLM's base price is the peak one; its off_peak_pricing is not.
		litellm: `{"deepseek/deepseek-v4-pro": {"litellm_provider": "deepseek", "mode": "chat", "max_output_tokens": 393216,
			"input_cost_per_token": 1.32e-06, "output_cost_per_token": 3.96e-06,
			"off_peak_pricing": {"input_cost_per_token": 6.6e-07, "output_cost_per_token": 1.98e-06}}}`,
		// genai-prices lists both, by time window, in either order.
		genai: `[{"id": "deepseek", "models": [
			{"id": "deepseek-v4-pro", "prices": [
				{"prices": {"input_mtok": 0.66, "output_mtok": 1.98}},
				{"constraint": {"start_time": "01:00:00Z", "end_time": "04:00:00Z"}, "prices": {"input_mtok": 1.32, "output_mtok": 3.96}}]},
			{"id": "deepseek-only-genai", "prices": [
				{"constraint": {"start_time": "01:00:00Z", "end_time": "04:00:00Z"}, "prices": {"input_mtok": 2, "output_mtok": 4}},
				{"prices": {"input_mtok": 1, "output_mtok": 2}}]}]}]`,
		// models.dev holds only the off-peak price, half of peak.
		modelsdev: `{"deepseek": {"models": {
			"deepseek-v4-pro": {"cost": {"input": 0.66, "output": 1.98}, "limit": {"output": 393216}},
			"deepseek-only-genai": {"cost": {"input": 1, "output": 2}, "limit": {"output": 99}},
			"deepseek-only-modelsdev": {"cost": {"input": 0.5, "output": 1}, "limit": {"output": 99}}}}}`,
	})
	e := d.Presets["deepseek"]["deepseek-v4-pro"]
	if e.Input != 1320000 || e.Output != 3960000 {
		t.Errorf("deepseek-v4-pro = %s; want the peak $1.32/$3.96", show(e))
	}
	if e := d.Presets["deepseek"]["deepseek-only-genai"]; e.Input != 2000000 || e.Output != 4000000 || e.MaxOutput != 99 {
		t.Errorf("deepseek-only-genai = %s; want peak prices and models.dev's limit", show(e))
	}
	if _, ok := d.Presets["deepseek"]["deepseek-only-modelsdev"]; ok {
		t.Error("a DeepSeek model priced only by models.dev has only an off-peak price, and must be left out")
	}
}

func TestPriceHistoryUsesTheNewestPriceInForce(t *testing.T) {
	d, _ := gen(t, sources{genai: `[{"id": "openai", "models": [{"id": "gpt-h", "prices": [
		{"prices": {"input_mtok": 9, "output_mtok": 9}},
		{"constraint": {"start_date": "2026-01-01"}, "prices": {"input_mtok": 1, "output_mtok": 2}},
		{"constraint": {"start_date": "2099-01-01"}, "prices": {"input_mtok": 50, "output_mtok": 50}}],
		"price_variants": [
			{"when": {"service_tier": "default"}, "prices": {}},
			{"when": {"service_tier": "flex"}, "prices": {"input_mtok": 0.5}, "constraint": {"start_date": "2026-02-01"}},
			{"when": {"service_tier": "fast"}, "prices": {"input_mtok": 70}, "constraint": {"start_date": "2099-01-01"}},
			{"when": {"processing": "batch"}, "prices": {"input_mtok": 0.25}},
			{"when": {"route": "mistral/eu"}, "prices": {"input_mtok": 80}}
		]}]}]`,
		modelsdev: `{"openai": {"models": {"gpt-h": {"limit": {"output": 1}}}}}`})
	e := d.Global["gpt-h"]
	if e.Input != 1000000 || e.Output != 2000000 {
		t.Errorf("gpt-h = %s; want the 2026 price", show(e))
	}
	if len(e.Tiers) != 2 || e.Tiers["flex"].Input != 500000 || e.Tiers["flex"].Output != 2000000 || e.Tiers["batch"].Input != 250000 {
		t.Errorf("tiers = %s; want flex and batch, each inheriting what it doesn't state", show(e))
	}
}

func TestEveryDatedIDAndAliasIsIncludedAndNothingIsInvented(t *testing.T) {
	d, _ := gen(t, sources{
		litellm: `{"gpt-5.4-2026-03-05": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"openai/gpt-prefixed": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5}}`,
		genai: `[{"id": "openai", "models": [{"id": "gpt-5.4", "match": {"or": [{"equals": "gpt-5.4"}, {"equals": "gpt-5.4-2026-03-05"}, {"equals": "gpt-5-4"}, {"starts_with": "gpt-5.4-"}]}, "prices": {"input_mtok": 2.5, "output_mtok": 15}}]}]`,
		modelsdev: `{"openai": {"models": {"gpt-5.4": {"limit": {"output": 128000}}, "gpt-5-4": {"limit": {"output": 128000}},
			"gpt-5.4-2026-03-05": {"limit": {"output": 128000}}}}}`,
	})
	for _, id := range []string{"gpt-5.4", "gpt-5.4-2026-03-05", "gpt-5-4", "gpt-prefixed"} {
		if _, ok := d.Global[id]; !ok {
			t.Errorf("%s is missing; have %v", id, keys(d.Global))
		}
	}
	if len(d.Global) != 4 {
		t.Errorf("have %v; a prefix must not be turned into ids", keys(d.Global))
	}
	if got := d.Global["gpt-5.4-2026-03-05"].Input; got != 2500000 {
		t.Errorf("the dated id costs %d, want the higher of its sources", got)
	}
}

func TestPresetIDsAreThePresetsOwn(t *testing.T) {
	d, _ := gen(t, sources{
		litellm: `{
			"gemini/gemini-9": {"litellm_provider": "gemini", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"groq/openai/gpt-oss-120b": {"litellm_provider": "groq", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"together_ai/openai/gpt-oss-120b": {"litellm_provider": "together_ai", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"fireworks_ai/accounts/fireworks/models/x": {"litellm_provider": "fireworks_ai", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"mistral/mistral-9": {"litellm_provider": "mistral", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"cerebras/gpt-oss-120b": {"litellm_provider": "cerebras", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"azure/gpt-5.4": {"litellm_provider": "azure", "mode": "chat", "input_cost_per_token": 9e-06, "max_output_tokens": 5},
			"claude-9": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5}
		}`,
		genai: `[{"id": "openrouter", "models": [{"id": "openai/gpt-9", "prices": {"input_mtok": 2, "output_mtok": 3}}]},
			{"id": "aws", "models": [{"id": "nova", "prices": {"input_mtok": 2}}]}]`,
		modelsdev: `{"openrouter": {"models": {"openai/gpt-9": {"limit": {"output": 7}}}}, "poe": {"models": {"m": {"limit": {"output": 7}, "cost": {"input": 1}}}}}`,
	})
	want := map[string]string{
		"gemini": "gemini-9", "groq": "openai/gpt-oss-120b", "together": "openai/gpt-oss-120b",
		"fireworks": "accounts/fireworks/models/x", "mistral": "mistral-9", "cerebras": "gpt-oss-120b",
		"openrouter": "openai/gpt-9",
	}
	for preset, id := range want {
		if _, ok := d.Presets[preset][id]; !ok {
			t.Errorf("preset %s has %v, want %s", preset, keys(d.Presets[preset]), id)
		}
	}
	if len(d.Presets) != len(want) {
		t.Errorf("sections %v; a host with no preset gets none", d.Presets)
	}
	if _, ok := d.Global["claude-9"]; !ok {
		t.Errorf("Anthropic's models are global; have %v", keys(d.Global))
	}
}

func TestRowsKmanDoesNotMeterAreLeftOut(t *testing.T) {
	d, r := gen(t, sources{
		litellm: `{
			"sample_spec": {"litellm_provider": "one of https://docs.litellm.ai/docs/providers", "mode": "one of: chat", "input_cost_per_token": 0.0, "max_output_tokens": "max output tokens"},
			"dall-e-9": {"litellm_provider": "openai", "mode": "image_generation", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
			"whisper-9": {"litellm_provider": "openai", "mode": "audio_transcription", "input_cost_per_second": 0.0001},
			"embed-9": {"litellm_provider": "openai", "mode": "embedding", "input_cost_per_token": 1e-07},
			"no-limit": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 1e-06},
			"no-price": {"litellm_provider": "openai", "mode": "chat", "max_output_tokens": 5}
		}`,
		modelsdev: `{"openai": {"models": {"dall-e-9": {"cost": {"input": 1, "output": 1}, "limit": {"output": 5}}}}}`,
		genai:     `[{"id": "openai", "models": [{"id": "whisper-9", "prices": {"input_mtok": 1, "output_mtok": 1}}]}]`,
	})
	if len(d.Global) != 1 {
		t.Errorf("have %v, want only embed-9", keys(d.Global))
	}
	if _, ok := d.Global["embed-9"]; !ok {
		t.Errorf("have %v, want embed-9: an embedding needs no max output", keys(d.Global))
	}
	joined := strings.Join(r.Dropped, "\n")
	for _, id := range []string{"no-limit", "no-price"} {
		if !strings.Contains(joined, id) {
			t.Errorf("%s is not reported as dropped: %v", id, r.Dropped)
		}
	}
}

func TestReviewedMaxOutputWinsInBothDirections(t *testing.T) {
	d, r := gen(t, sources{
		litellm: `{
			"gpt-4.1": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "max_input_tokens": 1000000, "max_output_tokens": 1000000},
			"cerebras/gpt-oss-120b": {"litellm_provider": "cerebras", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 32768},
			"other": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5}
		}`,
		reviewed: `{
			"openai": {"gpt-4.1": {"max_output": 32768, "checked": "2026-11-03", "source": "https://example.test/gpt-4.1"},
				"gone-model": {"max_output": 1, "checked": "2026-11-03", "source": "https://example.test/gone"}},
			"cerebras": {"gpt-oss-120b": {"max_output": 40960, "checked": "2026-11-03", "source": "https://example.test/oss"}}
		}`,
	})
	if e := d.Global["gpt-4.1"]; e.MaxOutput != 32768 || !e.Reviewed {
		t.Errorf("understated by sources? gpt-4.1 = %s; want the reviewed 32,768 under the source's 1M", show(e))
	}
	if e := d.Presets["cerebras"]["gpt-oss-120b"]; e.MaxOutput != 40960 || !e.Reviewed {
		t.Errorf("cerebras = %s; want the reviewed 40,960 over the source's 32,768", show(e))
	}
	if e := d.Global["other"]; e.Reviewed {
		t.Errorf("other = %s; not reviewed", show(e))
	}
	if !strings.Contains(strings.Join(r.ReviewedMissing, " "), "gone-model") {
		t.Errorf("a reviewed model no source lists must be reported: %v", r.ReviewedMissing)
	}
}

func TestAReviewedEntryMustSayWhereItWasChecked(t *testing.T) {
	for _, body := range []string{
		`{"openai": {"gpt-x": {"max_output": 5, "source": "https://example.test"}}}`,
		`{"openai": {"gpt-x": {"max_output": 5, "checked": "2026-11-03"}}}`,
		`{"openai": {"gpt-x": {"checked": "2026-11-03", "source": "https://example.test"}}}`,
		`{"nowhere": {"gpt-x": {"max_output": 5, "checked": "2026-11-03", "source": "https://example.test"}}}`,
		`{"openai": {"gpt-x": {"max_output": 5, "checked": "2026-11-03", "source": "https://example.test", "typo": 1}}}`,
	} {
		if _, _, err := generate(sources{reviewed: body}.inputs(), now); err == nil {
			t.Errorf("generate accepted reviewed file %s", body)
		}
	}
}

func TestAnEmptySourceIsRefused(t *testing.T) {
	for name, s := range map[string]sources{
		"litellm":   {litellm: `{}`},
		"genai":     {genai: `[]`},
		"modelsdev": {modelsdev: `{}`},
		"garbled":   {genai: `{"not": "a list"}`},
		"no prices": {litellm: `{"sample_spec": {"mode": "one of: chat"}}`},
	} {
		// The fixtures above use an empty source to focus a test. A real run
		// checks first, which a format change or a truncated download must
		// not get past.
		if err := checkSources(s.inputs(), now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSourcesAndTimeAreRecorded(t *testing.T) {
	d, _ := gen(t, sources{})
	if !d.GeneratedAt.Equal(now) || d.Schema != pricing.Schema {
		t.Errorf("header = %v, schema %d", d.GeneratedAt, d.Schema)
	}
	if len(d.Sources) != 3 {
		t.Fatalf("sources = %+v", d.Sources)
	}
	for _, s := range d.Sources {
		if s.URL == "" || len(s.SHA256) != 64 {
			t.Errorf("source = %+v", s)
		}
	}
}

func TestTheOutputIsDeterministicAndHasOneModelPerLine(t *testing.T) {
	s := sources{litellm: `{
		"b": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
		"a": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5},
		"groq/c": {"litellm_provider": "groq", "mode": "chat", "input_cost_per_token": 1e-06, "max_output_tokens": 5}}`}
	d, _ := gen(t, s)
	first, err := encode(d)
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := gen(t, s)
	second, _ := encode(d2)
	if string(first) != string(second) {
		t.Error("two runs differ")
	}
	if _, err := pricing.Parse(first); err != nil {
		t.Fatalf("Parse(encode(d)): %v\n%s", err, first)
	}
	text := string(first)
	for _, id := range []string{`"a":`, `"b":`, `"c":`} {
		var lines int
		for _, l := range strings.Split(text, "\n") {
			if strings.Contains(l, id) {
				lines++
			}
		}
		if lines != 1 {
			t.Errorf("%s is on %d lines of\n%s", id, lines, text)
		}
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
