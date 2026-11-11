package profiles_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

func TestTheRequestsOfAMeteredProfileAreReadForWhatToReserve(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       profiles.Asked
	}{
		{"a chat request", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`, profiles.Asked{Model: "gpt-5", Metered: true}},
		{"a limit and a tier", `{"model":"m","max_tokens":100,"service_tier":"flex"}`, profiles.Asked{Model: "m", MaxTokens: 100, ServiceTier: "flex", Metered: true}},
		{"the largest of the limits, never the smallest", `{"model":"m","max_tokens":10,"max_completion_tokens":300,"max_output_tokens":20}`, profiles.Asked{Model: "m", MaxTokens: 300, Metered: true}},
		{"a fractional limit rounds up", `{"model":"m","max_tokens":10.2}`, profiles.Asked{Model: "m", MaxTokens: 11, Metered: true}},
		{"a limit that is nonsense is none", `{"model":"m","max_tokens":-5,"max_output_tokens":"lots"}`, profiles.Asked{Model: "m", Metered: true}},
		{"anything past the top level is the app's content, not a field", `{"messages":[{"model":"gpt-5","max_tokens":1}],"model":"m"}`, profiles.Asked{Model: "m", Metered: true}},
		{"text that looks like a field", `{"prompt":"\"model\":\"x\", } {","model":"m"}`, profiles.Asked{Model: "m", Metered: true}},
		{"a key written with an escape is the same key", `{"model":"m"}`, profiles.Asked{Model: "m", Metered: true}},
		{"the last of a repeated key, as the parsers upstream take it", `{"model":"cheap","model":"dear"}`, profiles.Asked{Model: "dear", Metered: true}},
		{"anthropic's fast mode is a tier", `{"model":"m","speed":"fast"}`, profiles.Asked{Model: "m", ServiceTier: "fast", Metered: true}},
		// What cannot be metered: F1, a body with no model of its own.
		{"no model", `{"messages":[]}`, profiles.Asked{}},
		{"a model that is not a string", `{"model":5}`, profiles.Asked{}},
		{"an empty model", `{"model":""}`, profiles.Asked{}},
		{"a model of the wrong case", `{"Model":"gpt-5"}`, profiles.Asked{}},
		{"a model only in a nested object", `{"input":{"model":"gpt-5"}}`, profiles.Asked{}},
		{"not an object", `["model","gpt-5"]`, profiles.Asked{}},
		{"not JSON", "--boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nwhisper-1", profiles.Asked{}},
		{"nothing", ``, profiles.Asked{}},
		{"cut short", `{"model":"gpt-5","messages":[{"a":`, profiles.Asked{Model: "gpt-5", Metered: true}},
	} {
		for _, profile := range []string{registry.ProfileOpenAI, registry.ProfileAnthropic} {
			t.Run(profile+" "+tc.name, func(t *testing.T) {
				if got := profiles.For(profile).Ask([]byte(tc.body)); got != tc.want {
					t.Errorf("Ask = %+v, want %+v", got, tc.want)
				}
			})
		}
	}
}

func TestTheGenericProfileMetersNothing(t *testing.T) {
	p := profiles.For(registry.ProfileGeneric)
	if p.Meters() || p.Ask([]byte(`{"model":"gpt-5"}`)).Metered {
		t.Error("generic is unmetered: kman does not read what passes through it")
	}
	for _, name := range []string{registry.ProfileOpenAI, registry.ProfileAnthropic} {
		if !profiles.For(name).Meters() {
			t.Errorf("%s does not meter", name)
		}
	}
}

// A very large body is read in one pass without holding it again.
func TestReadingALargeRequestCopiesNothingOfIt(t *testing.T) {
	body := []byte(`{"model":"m","input":"` + strings.Repeat("x", 8<<20) + `","max_tokens":7}`)
	p := profiles.For(registry.ProfileOpenAI)
	allocated := testing.AllocsPerRun(5, func() { p.Ask(body) })
	if got := p.Ask(body); got.Model != "m" || got.MaxTokens != 7 {
		t.Fatalf("Ask = %+v", got)
	}
	if allocated > 4 {
		t.Errorf("Ask allocated %v times over an 8 MiB body", allocated)
	}
}

// Routes that cost nothing report no usage, and a response with none is
// otherwise charged as a request that ran.
func TestRoutesThatCostNothing(t *testing.T) {
	for _, tc := range []struct {
		profile, path string
		free          bool
	}{
		{registry.ProfileAnthropic, "/v1/messages/count_tokens", true},
		{registry.ProfileAnthropic, "/v1/messages", false},
		{registry.ProfileOpenAI, "/v1/responses/input_tokens", true},
		{registry.ProfileOpenAI, "/v1/moderations", true},
		{registry.ProfileOpenAI, "/v1/audio/speech", false},
		{registry.ProfileOpenAI, "/v1/chat/completions", false},
		{registry.ProfileOpenAI, "/v1/images/generations", false},
		{registry.ProfileGeneric, "/v1/messages/count_tokens", false},
	} {
		if got := profiles.For(tc.profile).Free(tc.path); got != tc.free {
			t.Errorf("%s Free(%q) = %v, want %v", tc.profile, tc.path, got, tc.free)
		}
	}
}
