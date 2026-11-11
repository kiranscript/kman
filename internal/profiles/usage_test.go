package profiles_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

func jsonHeader() http.Header { return http.Header{"Content-Type": {"application/json"}} }
func sseHeader() http.Header {
	return http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}}
}

// read feeds a whole response to a profile's usage reader at once.
func read(t testing.TB, profile string, h http.Header, body string) profiles.Usage {
	t.Helper()
	r := profiles.For(profile).UsageReader(h)
	if r == nil {
		t.Fatalf("profile %s has no reader for %v", profile, h)
	}
	r.Feed([]byte(body))
	return r.Usage()
}

// Expected counts are worked by hand from the provider's meaning: OpenAI's
// prompt_tokens includes cached and audio tokens, Anthropic's input_tokens
// does not.
func TestUsageIsReadFromAJSONResponse(t *testing.T) {
	for _, tc := range []struct {
		name, profile, body string
		want                profiles.Usage
	}{
		{
			"openai chat", registry.ProfileOpenAI,
			`{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o-mini-2024-07-18","service_tier":"default",
			  "choices":[{"index":0,"message":{"role":"assistant","content":"hi"}}],
			  "usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,
			    "prompt_tokens_details":{"cached_tokens":30,"audio_tokens":10},
			    "completion_tokens_details":{"reasoning_tokens":5,"audio_tokens":4}}}`,
			profiles.Usage{Seen: true, Model: "gpt-4o-mini-2024-07-18", ServiceTier: "default",
				InputTokens: 60, CachedTokens: 30, AudioInputTokens: 10, OutputTokens: 16, AudioOutputTokens: 4},
		},
		{
			"openai embeddings, which have no completion", registry.ProfileOpenAI,
			`{"object":"list","data":[{"object":"embedding","index":0,"embedding":"AAAAPw=="}],"model":"text-embedding-3-small",
			  "usage":{"prompt_tokens":8,"total_tokens":8}}`,
			profiles.Usage{Seen: true, Model: "text-embedding-3-small", InputTokens: 8},
		},
		{
			"openai responses", registry.ProfileOpenAI,
			`{"id":"resp_1","object":"response","model":"gpt-5","service_tier":"priority","status":"completed",
			  "output":[{"type":"web_search_call","id":"ws_1"},{"type":"web_search_call","id":"ws_2"},
			    {"type":"file_search_call","id":"fs_1"},{"type":"code_interpreter_call","id":"ci_1"},
			    {"type":"message","content":[{"type":"web_search_call"}]}],
			  "usage":{"input_tokens":50,"input_tokens_details":{"cached_tokens":20},
			    "output_tokens":9,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":59}}`,
			profiles.Usage{Seen: true, Model: "gpt-5", ServiceTier: "priority", InputTokens: 30, CachedTokens: 20, OutputTokens: 9,
				Tools: []profiles.ToolCalls{{Name: "web_search", Calls: 2}, {Name: "file_search", Calls: 1}, {Name: "code_interpreter", Calls: 1}}},
		},
		{
			"cached audio is counted once", registry.ProfileOpenAI,
			`{"model":"gpt-4o-audio","usage":{"prompt_tokens":100,"completion_tokens":1,
			  "prompt_tokens_details":{"cached_tokens":30,"audio_tokens":20,"cached_tokens_details":{"text_tokens":25,"audio_tokens":5}}}}`,
			profiles.Usage{Seen: true, Model: "gpt-4o-audio", InputTokens: 55, CachedTokens: 25, AudioCachedTokens: 5, AudioInputTokens: 15, OutputTokens: 1},
		},
		{
			"deepseek's cache fields", registry.ProfileOpenAI,
			`{"model":"deepseek-chat","usage":{"prompt_tokens":100,"completion_tokens":7,"prompt_cache_hit_tokens":40,"prompt_cache_miss_tokens":60}}`,
			profiles.Usage{Seen: true, Model: "deepseek-chat", InputTokens: 60, CachedTokens: 40, OutputTokens: 7},
		},
		{
			// $0.0038 is 3800 microdollars exactly, which floating point gets wrong.
			"openrouter's reported cost", registry.ProfileOpenAI,
			`{"model":"openai/gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.0038}}`,
			profiles.Usage{Seen: true, Model: "openai/gpt-4o", InputTokens: 10, OutputTokens: 2, ReportedCost: true, ReportedMicro: 3800},
		},
		{
			"a reported cost of nothing is still reported", registry.ProfileOpenAI,
			`{"model":"free","usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0}}`,
			profiles.Usage{Seen: true, Model: "free", InputTokens: 10, OutputTokens: 2, ReportedCost: true},
		},
		{
			"a sliver of a cost rounds up", registry.ProfileOpenAI,
			`{"usage":{"prompt_tokens":1,"cost":1.5e-7}}`,
			profiles.Usage{Seen: true, InputTokens: 1, ReportedCost: true, ReportedMicro: 1},
		},
		{
			// docs.x.ai: 1 USD is 10^10 ticks, so 37,756,000 ticks is $0.0037756.
			"xai's ticks", registry.ProfileOpenAI,
			`{"model":"grok-4","usage":{"prompt_tokens":10,"completion_tokens":2,"cost_in_usd_ticks":37756000}}`,
			profiles.Usage{Seen: true, Model: "grok-4", InputTokens: 10, OutputTokens: 2, ReportedCost: true, ReportedMicro: 3776},
		},
		{
			"a cost that is not a number is no cost", registry.ProfileOpenAI,
			`{"model":"sonar","usage":{"prompt_tokens":10,"cost":{"total_cost":0.01}}}`,
			profiles.Usage{Seen: true, Model: "sonar", InputTokens: 10},
		},
		{
			"anthropic message", registry.ProfileAnthropic,
			`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}],
			  "usage":{"input_tokens":12,"output_tokens":34,"cache_read_input_tokens":56,"cache_creation_input_tokens":78,
			    "cache_creation":{"ephemeral_5m_input_tokens":70,"ephemeral_1h_input_tokens":8},
			    "service_tier":"priority","server_tool_use":{"web_search_requests":3,"web_fetch_requests":9}}}`,
			profiles.Usage{Seen: true, Model: "claude-opus-5-5", ServiceTier: "priority", InputTokens: 12, OutputTokens: 34,
				CachedTokens: 56, CacheWriteTokens: 70, CacheWrite1hTokens: 8, Tools: []profiles.ToolCalls{{Name: "web_search", Calls: 3}}},
		},
		{
			"anthropic with no breakdown of its cache writes", registry.ProfileAnthropic,
			`{"model":"claude-x","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":78}}`,
			profiles.Usage{Seen: true, Model: "claude-x", InputTokens: 1, OutputTokens: 2, CacheWriteTokens: 78},
		},
		{
			"anthropic's fast speed is the tier it is priced at", registry.ProfileAnthropic,
			`{"model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2,"service_tier":"standard","speed":"fast"}}`,
			profiles.Usage{Seen: true, Model: "claude-opus-5-5", ServiceTier: "fast", InputTokens: 1, OutputTokens: 2},
		},
		{
			"anthropic's count_tokens answer is not usage", registry.ProfileAnthropic,
			`{"input_tokens":2095}`,
			profiles.Usage{},
		},
		{
			"an error names no usage", registry.ProfileOpenAI,
			`{"error":{"message":"no","type":"invalid_request_error"}}`,
			profiles.Usage{},
		},
		{
			"usage on an error is still usage", registry.ProfileAnthropic,
			`{"error":{"type":"overloaded_error"},"model":"claude-x","usage":{"input_tokens":3,"output_tokens":0}}`,
			profiles.Usage{Seen: true, Model: "claude-x", InputTokens: 3},
		},
		{
			"a null usage", registry.ProfileOpenAI,
			`{"model":"m","usage":null}`,
			profiles.Usage{Model: "m"},
		},
		{
			"cached over the total leaves no negative count", registry.ProfileOpenAI,
			`{"usage":{"prompt_tokens":5,"prompt_tokens_details":{"cached_tokens":9}}}`,
			profiles.Usage{Seen: true, CachedTokens: 9},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := read(t, tc.profile, jsonHeader(), tc.body); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("usage = %+v\n want   %+v", got, tc.want)
			}
		})
	}
}

// What a model writes, or what an API nests, must not be taken for what the
// upstream reports: that would let a prompt set its own price.
func TestOnlyTheResponsesOwnFieldsCount(t *testing.T) {
	for name, body := range map[string]string{
		"in the model's text": `{"model":"real","choices":[{"message":{"content":"\"usage\":{\"prompt_tokens\":999},\"model\":\"fake\""}}],"usage":{"prompt_tokens":1}}`,
		"nested in metadata":  `{"model":"real","metadata":{"model":"fake","usage":{"prompt_tokens":999}},"usage":{"prompt_tokens":1}}`,
		"nested in an array":  `{"model":"real","choices":[{"model":"fake","usage":{"prompt_tokens":999}}],"usage":{"prompt_tokens":1}}`,
		"a string in a list":  `{"model":"real","tags":["usage","model",{"usage":{"prompt_tokens":999}}],"usage":{"prompt_tokens":1}}`,
		"inside an escape":    `{"model":"real","note":"\\\"usage\\\":{\"","usage":{"prompt_tokens":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := read(t, registry.ProfileOpenAI, jsonHeader(), body)
			want := profiles.Usage{Seen: true, Model: "real", InputTokens: 1}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("usage = %+v", got)
			}
		})
	}
}

// A provider's odd field must not cost the rest of the usage.
func TestAMistypedFieldLosesOnlyThatField(t *testing.T) {
	got := read(t, registry.ProfileOpenAI, jsonHeader(),
		`{"model":"m","service_tier":7,"usage":{"prompt_tokens":10,"completion_tokens":"many","prompt_tokens_details":"none","total_tokens":-1}}`)
	want := profiles.Usage{Seen: true, Model: "m", InputTokens: 10}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("usage = %+v", got)
	}
}

// A count written as a float is still a count, and a negative one is none.
func TestCountsAreReadLeniently(t *testing.T) {
	got := read(t, registry.ProfileAnthropic, jsonHeader(), `{"usage":{"input_tokens":12.0,"output_tokens":-4,"cache_read_input_tokens":1e400}}`)
	if got.InputTokens != 12 || got.OutputTokens != 0 || got.CachedTokens != 0 {
		t.Errorf("usage = %+v", got)
	}
}

func TestAResponseKmanCannotReadHasNoReader(t *testing.T) {
	for _, h := range []http.Header{
		{"Content-Type": {"audio/mpeg"}},
		{"Content-Type": {"text/html"}},
		{},
		{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
		{"Content-Type": {"text/event-stream"}, "Content-Encoding": {"br"}},
	} {
		for _, name := range []string{registry.ProfileOpenAI, registry.ProfileAnthropic} {
			if profiles.For(name).UsageReader(h) != nil {
				t.Errorf("profile %s reads %v", name, h)
			}
		}
	}
	for _, h := range []http.Header{
		{"Content-Type": {"application/json; charset=utf-8"}},
		{"Content-Type": {"Application/JSON"}},
		{"Content-Type": {"application/json"}, "Content-Encoding": {"identity"}},
		{"Content-Type": {"TEXT/EVENT-STREAM"}},
	} {
		if profiles.For(registry.ProfileOpenAI).UsageReader(h) == nil {
			t.Errorf("no reader for %v", h)
		}
	}
}

// SPEC §6: the generic profile is unmetered.
func TestTheGenericProfileReadsNoUsage(t *testing.T) {
	if profiles.For(registry.ProfileGeneric).UsageReader(jsonHeader()) != nil {
		t.Error("the generic profile has a usage reader")
	}
}
