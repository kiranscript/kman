package profiles_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

// fuzzRead holds a reader to what must be true of any response at all: it doesn't
// panic, no count is negative, and the answer doesn't depend on how the response
// was split.
func fuzzRead(t *testing.T, profile string, h http.Header, body []byte, split uint) {
	whole := profiles.For(profile).UsageReader(h)
	whole.Feed(body)
	u := whole.Usage()
	for _, n := range []int64{u.InputTokens, u.OutputTokens, u.CachedTokens, u.CacheWriteTokens, u.CacheWrite1hTokens,
		u.AudioInputTokens, u.AudioOutputTokens, u.AudioCachedTokens, u.ReportedMicro} {
		if n < 0 {
			t.Errorf("a negative count: %+v", u)
		}
	}
	for _, c := range u.Tools {
		if c.Calls <= 0 {
			t.Errorf("a tool with no calls: %+v", u)
		}
	}
	at := 0
	if len(body) > 0 {
		at = int(split % uint(len(body)+1))
	}
	parts := profiles.For(profile).UsageReader(h)
	parts.Feed(body[:at])
	parts.Feed(body[at:])
	if got := parts.Usage(); !reflect.DeepEqual(got, u) {
		t.Errorf("split at %d: %+v\nwhole: %+v", at, got, u)
	}
	singly := profiles.For(profile).UsageReader(h)
	for i := range body {
		singly.Feed(body[i : i+1])
	}
	if got := singly.Usage(); !reflect.DeepEqual(got, u) {
		t.Errorf("a byte at a time: %+v\nwhole: %+v", got, u)
	}
}

func profileFrom(b bool) string {
	if b {
		return registry.ProfileAnthropic
	}
	return registry.ProfileOpenAI
}

func FuzzUsageFromJSON(f *testing.F) {
	for _, seed := range []string{
		`{"model":"m","service_tier":"flex","usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30,"audio_tokens":10,"cached_tokens_details":{"audio_tokens":5}},"completion_tokens_details":{"audio_tokens":4},"cost":0.0038,"cost_in_usd_ticks":37756000}}`,
		`{"model":"m","output":[{"type":"web_search_call"},{"type":"message","content":[]},7],"usage":{"input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":3},"server_tool_use":{"web_search_requests":2},"speed":"fast"}}`,
		`{"response":{"model":"m","response":{"usage":{}},"usage":{"input_tokens":1}},"message":{"usage":null}}`,
		`{"usage":{"cost":1e-999999,"input_tokens":1e999}}{"usage":{"cost":-1}}`,
		`{"a":"\"","b\"":{"usage":1},"usage":{"input_tokens":1}}`,
		`[{"usage":{}}]`, `}}}]]]`, `{"usage"`, `{"usage":`, `{"output":[`, `{"output":[{"type":`, ``,
	} {
		f.Add([]byte(seed), false, uint(7))
		f.Add([]byte(seed), true, uint(11))
	}
	f.Fuzz(func(t *testing.T, body []byte, anthropic bool, split uint) {
		fuzzRead(t, profileFrom(anthropic), jsonHeader(), body, split)
	})
}

func FuzzUsageFromStream(f *testing.F) {
	for _, seed := range []string{openAIStream, responsesStream, anthropicStream,
		"data: {\"usage\":\r\ndata: {\"prompt_tokens\":1}}\r\n\r\n",
		"\r\r\n\n:x\ndata\ndata:\ndata: \n\n", "data: [DONE]", "data", "event: x", "\xef\xbb\xbfdata: {}\n\n",
	} {
		f.Add([]byte(seed), false, uint(7))
		f.Add([]byte(seed), true, uint(11))
	}
	f.Fuzz(func(t *testing.T, body []byte, anthropic bool, split uint) {
		fuzzRead(t, profileFrom(anthropic), sseHeader(), body, split)
	})
}

// Whatever an app sends, a stream that goes on is asked for its usage, and
// nothing else in the request changes meaning. A body that's left alone is left
// alone byte for byte.
func FuzzAskingForUsage(f *testing.F) {
	for _, seed := range []string{
		`{"stream":true}`, `{"stream":false}`, `{"stream":1}`, `{"stream":"x"}`, `{ "stream" : true } `,
		`{"stream":true,"stream_options":null}`, `{"stream":true,"stream_options":{"include_usage":false,"x":[1,{"y":2}]}}`,
		`{"stream":true,"stream_options":{"include_usage":true,"include_usage":false}}`, `{"stream":true,"stream_options":7}`,
		`{"stream":true,"stream":false}`, `{"a":12345678901234567890,"stream":true}`, `[]`, `null`, ``, `{`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		orig := string(body)
		got := profiles.For(registry.ProfileOpenAI).Prepare(http.Header{}, "/v1/chat/completions", body, true)
		if string(body) != orig {
			t.Fatalf("the body given was changed: %q", body)
		}
		var before map[string]json.RawMessage
		if json.Unmarshal(body, &before) != nil || before == nil {
			if string(got) != orig {
				t.Errorf("an unreadable body changed: %q to %q", orig, got)
			}
			return
		}
		var after map[string]json.RawMessage
		if err := json.Unmarshal(got, &after); err != nil || after == nil {
			t.Fatalf("%q became %q: %v", orig, got, err)
		}
		stream, streaming := before["stream"]
		if !streaming || string(stream) == "false" || string(stream) == "null" {
			if string(got) != orig {
				t.Errorf("a request with no stream changed: %q to %q", orig, got)
			}
			return
		}
		for name, was := range before {
			now, ok := after[name]
			if !ok {
				t.Errorf("%q lost %s", orig, name)
			}
			if name != "stream_options" && !equalJSON(t, was, now) {
				t.Errorf("%s went from %s to %s", name, was, now)
			}
		}
		var options map[string]json.RawMessage
		if json.Unmarshal(before["stream_options"], &options) != nil && string(before["stream_options"]) != "null" && before["stream_options"] != nil {
			return // options that aren't an object, for the upstream to refuse
		}
		var asked struct {
			Usage bool `json:"include_usage"`
		}
		if err := json.Unmarshal(after["stream_options"], &asked); err != nil || !asked.Usage {
			t.Errorf("%q became %q, which doesn't ask for usage", orig, got)
		}
	})
}

func equalJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(x, y)
}
