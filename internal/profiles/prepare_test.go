package profiles_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

const chat = "/v1/chat/completions"

func prepare(profile, path, body string, inject bool) (http.Header, string) {
	h := http.Header{}
	return h, string(profiles.For(profile).Prepare(h, path, []byte(body), inject))
}

func TestAStreamIsAskedForItsUsage(t *testing.T) {
	for _, tc := range []struct{ name, path, body, want string }{
		{"added at the end, leaving the rest byte for byte", chat, `{ "model" : "m",  "stream":true }`, `{ "model" : "m",  "stream":true ,"stream_options":{"include_usage":true}}`},
		{"chat under another base path", "/openai/v1/chat/completions", `{"stream":true}`, `{"stream":true,"stream_options":{"include_usage":true}}`},
		{"completions", "/v1/completions", `{"stream":true}`, `{"stream":true,"stream_options":{"include_usage":true}}`},
		{"a key written with an escape is the same key", chat, `{"stream":true}`, `{"stream":true,"stream_options":{"include_usage":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := prepare(registry.ProfileOpenAI, tc.path, tc.body, true); got != tc.want {
				t.Errorf("body = %s\n want  %s", got, tc.want)
			}
		})
	}
}

// When the app has stream_options of its own, what matters is the meaning, as
// JSON: its other options stay, and include_usage ends up true.
func TestAnAppsStreamOptionsAreKept(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"null", `{"stream":true,"stream_options":null}`, `{"stream":true,"stream_options":{"include_usage":true}}`},
		{"another option", `{"stream":true,"stream_options":{"include_obfuscation":false}}`, `{"stream":true,"stream_options":{"include_obfuscation":false,"include_usage":true}}`},
		// Turning usage off would be a way not to be metered.
		{"usage turned off", `{"stream":true,"stream_options":{"include_usage":false}}`, `{"stream":true,"stream_options":{"include_usage":true}}`},
		{"the last of a repeated option is the one that counts", `{"stream":true,"stream_options":{"include_usage":true,"include_usage":false}}`, `{"stream":true,"stream_options":{"include_usage":true}}`},
		{"a repeated object", `{"stream":true,"stream_options":{"a":1},"stream_options":{"include_usage":false}}`, `{"stream":true,"stream_options":{"include_usage":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := prepare(registry.ProfileOpenAI, chat, tc.body, true)
			var a, b any
			if err := json.Unmarshal([]byte(got), &a); err != nil {
				t.Fatalf("%v: %s", err, got)
			}
			if err := json.Unmarshal([]byte(tc.want), &b); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("body = %s\n want  %s", got, tc.want)
			}
		})
	}
}

// Rewriting must not change a value: a seed past 2^53, or text with characters
// JSON encoders like to escape.
func TestRewritingKeepsEveryValueExactly(t *testing.T) {
	_, got := prepare(registry.ProfileOpenAI, chat,
		`{"seed":12345678901234567890,"t":1.10,"text":"<a&b>é","stream":true,"stream_options":{}}`, true)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"seed": "12345678901234567890", "t": "1.10", "text": `"<a&b>é"`, "stream_options": `{"include_usage":true}`} {
		if string(fields[key]) != want {
			t.Errorf("%s = %s, want %s", key, fields[key], want)
		}
	}
}

func TestABodyIsLeftAloneWhenThereIsNothingToAskFor(t *testing.T) {
	for _, tc := range []struct {
		name, profile, path, body string
		inject                    bool
	}{
		{"the preset doesn't allow it", registry.ProfileOpenAI, chat, `{"stream":true}`, false},
		{"no stream", registry.ProfileOpenAI, chat, `{"model":"m"}`, true},
		{"stream false", registry.ProfileOpenAI, chat, `{"stream":false}`, true},
		{"stream null", registry.ProfileOpenAI, chat, `{"stream":null}`, true},
		{"already asked", registry.ProfileOpenAI, chat, `{"stream":true, "stream_options": {"include_usage":true}}`, true},
		// The Responses API always reports usage, and refuses this option as unknown.
		{"the responses api", registry.ProfileOpenAI, "/v1/responses", `{"stream":true}`, true},
		{"embeddings", registry.ProfileOpenAI, "/v1/embeddings", `{"stream":true}`, true},
		{"not json", registry.ProfileOpenAI, chat, `stream:true`, true},
		{"not an object", registry.ProfileOpenAI, chat, `[{"stream":true}]`, true},
		{"trailing junk", registry.ProfileOpenAI, chat, `{"stream":true} x`, true},
		{"options that aren't an object", registry.ProfileOpenAI, chat, `{"stream":true,"stream_options":"x"}`, true},
		{"no body", registry.ProfileOpenAI, chat, ``, true},
		{"anthropic reports usage unasked", registry.ProfileAnthropic, "/v1/messages", `{"stream":true}`, true},
		{"generic reads nothing", registry.ProfileGeneric, chat, `{"stream":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := prepare(tc.profile, tc.path, tc.body, tc.inject); got != tc.body {
				t.Errorf("body = %s", got)
			}
		})
	}
}

// A compressed response would hide its usage from the reader, and an app could
// ask for one for that reason alone. So a metered request asks for none.
func TestAMeteredRequestAsksForAnUncompressedResponse(t *testing.T) {
	for _, name := range []string{registry.ProfileOpenAI, registry.ProfileAnthropic} {
		h := http.Header{"Accept-Encoding": {"gzip, br"}}
		profiles.For(name).Prepare(h, "/v1/x", nil, false)
		if got := h.Values("Accept-Encoding"); len(got) != 1 || got[0] != "identity" {
			t.Errorf("profile %s: Accept-Encoding = %q", name, got)
		}
	}
	h := http.Header{"Accept-Encoding": {"gzip, br"}}
	profiles.For(registry.ProfileGeneric).Prepare(h, "/v1/x", nil, false)
	if got := h.Get("Accept-Encoding"); got != "gzip, br" {
		t.Errorf("generic: Accept-Encoding = %q", got)
	}
}

// The body it was given belongs to the gateway, which may read it again.
func TestPrepareDoesNotChangeTheBodyItWasGiven(t *testing.T) {
	body := make([]byte, 0, 256)
	body = append(body, `{"stream":true}`...)
	got := profiles.For(registry.ProfileOpenAI).Prepare(http.Header{}, chat, body, true)
	if string(body) != `{"stream":true}` || string(body[:cap(body)][len(body):len(body)+1]) != "\x00" {
		t.Errorf("the original now holds %q", body[:cap(body)])
	}
	if string(got) != `{"stream":true,"stream_options":{"include_usage":true}}` {
		t.Errorf("got %s", got)
	}
}
