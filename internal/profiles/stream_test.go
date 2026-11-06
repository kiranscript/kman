package profiles_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

const openAIStream = `data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-4o-mini","service_tier":"default","choices":[{"delta":{"content":"\"usage\":{\"prompt_tokens\":999}"}}],"usage":null}

data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{},"finish_reason":"stop"}],"usage":null}

data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4}}}

data: [DONE]

`

const responsesStream = `event: response.created
data: {"type":"response.created","response":{"id":"r1","model":"gpt-5","status":"in_progress","output":[],"usage":null}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"hi","response_id":"r1"}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"r1","model":"gpt-5","service_tier":"flex","status":"completed","output":[{"type":"web_search_call","id":"ws_1"},{"type":"message","content":[]}],"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":5},"output_tokens":7,"total_tokens":27}}}

`

const anthropicStream = `event: message_start
data: {"type":"message_start","message":{"id":"m1","type":"message","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":25,"cache_read_input_tokens":10,"cache_creation_input_tokens":5,"output_tokens":1,"service_tier":"standard"}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"usage"}}

event: ping
data: {"type":"ping"}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}

`

var streams = []struct {
	name, profile, body string
	want                profiles.Usage
}{
	{"openai chat", registry.ProfileOpenAI, openAIStream, profiles.Usage{Seen: true, Model: "gpt-4o-mini", ServiceTier: "default", InputTokens: 6, CachedTokens: 4, OutputTokens: 3}},
	{"openai responses", registry.ProfileOpenAI, responsesStream, profiles.Usage{Seen: true, Model: "gpt-5", ServiceTier: "flex", InputTokens: 15, CachedTokens: 5, OutputTokens: 7,
		Tools: []profiles.ToolCalls{{Name: "web_search", Calls: 1}}}},
	{"anthropic", registry.ProfileAnthropic, anthropicStream, profiles.Usage{Seen: true, Model: "claude-opus-5-5", ServiceTier: "standard", InputTokens: 25, CachedTokens: 10, CacheWriteTokens: 5, OutputTokens: 42}},
}

func TestUsageIsReadFromAStream(t *testing.T) {
	for _, tc := range streams {
		t.Run(tc.name, func(t *testing.T) {
			if got := read(t, tc.profile, sseHeader(), tc.body); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("usage = %+v\n want   %+v", got, tc.want)
			}
		})
	}
}

// A network splits a stream anywhere, including inside a field's name, a number
// or an escape.
func TestUsageDoesNotDependOnHowAStreamIsSplit(t *testing.T) {
	for _, tc := range streams {
		t.Run(tc.name, func(t *testing.T) {
			for size := 1; size < 40; size++ {
				r := profiles.For(tc.profile).UsageReader(sseHeader())
				for rest := tc.body; rest != ""; {
					n := min(size, len(rest))
					r.Feed([]byte(rest[:n]))
					rest = rest[n:]
				}
				if got := r.Usage(); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("in chunks of %d: usage = %+v", size, got)
				}
			}
		})
	}
}

// What has been reported is there before the stream ends, so a stream that
// breaks off keeps it.
func TestUsageIsThereAsSoonAsItIsReported(t *testing.T) {
	r := profiles.For(registry.ProfileAnthropic).UsageReader(sseHeader())
	r.Feed([]byte(anthropicStream[:strings.Index(anthropicStream, "event: content_block_delta")]))
	if u := r.Usage(); !u.Seen || u.InputTokens != 25 || u.OutputTokens != 1 {
		t.Errorf("usage = %+v", u)
	}
	// Half an event's usage is no usage.
	r = profiles.For(registry.ProfileOpenAI).UsageReader(sseHeader())
	r.Feed([]byte(`data: {"choices":[],"usage":{"prompt_tokens":10,"comp`))
	if u := r.Usage(); u.Seen {
		t.Errorf("usage = %+v", u)
	}
}

func TestTheStreamFormatIsReadToTheLetter(t *testing.T) {
	const event = `{"model":"m","usage":{"prompt_tokens":1}}`
	want := profiles.Usage{Seen: true, Model: "m", InputTokens: 1}
	for name, body := range map[string]string{
		"crlf lines":             "data: " + event + "\r\n\r\n",
		"cr lines":               "data: " + event + "\r\r",
		"no space after data":    "data:" + event + "\n\n",
		"no final blank line":    "data: " + event,
		"a comment first":        ": keep-alive\n\ndata: " + event + "\n\n",
		"other fields":           "event: x\nid: 1\nretry: 5\ndata: " + event + "\n\n",
		"data split over lines":  "data: {\"model\":\"m\",\ndata: \"usage\":{\"prompt_tokens\":1}}\n\n",
		"a broken event first":   "data: {\"model\":\"m\",\"x\":{\n\ndata: " + event + "\n\n",
		"not json first":         "data: hello\n\ndata: " + event + "\n\n",
		"a field named like it":  "database: {\"model\":\"fake\"}\n\ndata: " + event + "\n\n",
		"a field ending like it": "metadata: {\"model\":\"fake\"}\n\ndata: " + event + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := read(t, registry.ProfileOpenAI, sseHeader(), body); !reflect.DeepEqual(got, want) {
				t.Errorf("usage = %+v", got)
			}
		})
	}
}

// A model's text can hold an event's worth of text, escaped, without becoming one.
func TestAModelsTextCannotMakeAnEvent(t *testing.T) {
	body := `data: {"model":"real","choices":[{"delta":{"content":"\n\ndata: {\"model\":\"fake\",\"usage\":{\"prompt_tokens\":999}}\n\n"}}]}` + "\n\n"
	got := read(t, registry.ProfileOpenAI, sseHeader(), body)
	if want := (profiles.Usage{Model: "real"}); !reflect.DeepEqual(got, want) {
		t.Errorf("usage = %+v", got)
	}
}

// The reader holds no more than a field: a response of any length goes through it
// without it growing.
func TestAVeryLongFieldIsSkippedNotHeld(t *testing.T) {
	huge := []byte(strings.Repeat("x", 1<<20))
	allocs := testing.AllocsPerRun(5, func() {
		r := profiles.For(registry.ProfileOpenAI).UsageReader(jsonHeader())
		r.Feed([]byte(`{"data":"`))
		for range 64 {
			r.Feed(huge)
		}
		r.Feed([]byte(`","model":"m","usage":{"prompt_tokens":1}}`))
		if u := r.Usage(); !u.Seen || u.InputTokens != 1 {
			t.Errorf("usage = %+v", u)
		}
	})
	if allocs > 20 {
		t.Errorf("allocated %v times", allocs)
	}
	// A wanted field over the limit is given up on, and the scan carries on.
	over := `{"usage":{"prompt_tokens":1,"pad":"` + strings.Repeat("x", 128<<10) + `"},"model":"m"}`
	if got := read(t, registry.ProfileOpenAI, jsonHeader(), over); got.Seen || got.Model != "m" {
		t.Errorf("usage = %+v", got)
	}
}

func BenchmarkUsageReaderStream(b *testing.B) {
	b.SetBytes(int64(len(openAIStream)))
	body := []byte(openAIStream)
	for b.Loop() {
		r := profiles.For(registry.ProfileOpenAI).UsageReader(sseHeader())
		r.Feed(body)
		r.Usage()
	}
}

func BenchmarkUsageReaderSkipping(b *testing.B) {
	body := []byte(`{"data":"` + strings.Repeat("x", 1<<20) + `","usage":{"prompt_tokens":1}}`)
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		r := profiles.For(registry.ProfileOpenAI).UsageReader(jsonHeader())
		r.Feed(body)
		r.Usage()
	}
}
