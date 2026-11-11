package policy_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/policy"
)

func TestModelOfReadsTheTopLevelModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{"plain", `{"model":"gpt-4o","messages":[]}`, "gpt-4o", true},
		{"after other fields", `{"messages":[{"model":"inner","role":"user"}],"model":"gpt-4o"}`, "gpt-4o", true},
		{"whitespace", " \n{ \"model\" :\t\"m\" }\n", "m", true},
		{"an inner model is not the model", `{"messages":[{"model":"inner"}]}`, "", false},
		{"a model inside a string is not the model", `{"text":"\"model\":\"x\"","other":1}`, "", false},
		{"an escaped key", `{"model":"m"}`, "m", true},
		{"an escaped value", `{"model":"a/b"}`, "a/b", true},
		{"nested braces in strings", `{"a":"}{","model":"m"}`, "m", true},
		{"an escaped quote in a string", `{"a":"\\\"","model":"m"}`, "m", true},
		{"no model", `{"input":"x"}`, "", false},
		{"an empty object", `{}`, "", false},

		// What upstream might read differently is no model at all.
		{"a repeated model", `{"model":"a","model":"b"}`, "", false},
		{"a repeated, identical model", `{"model":"a","model":"a"}`, "", false},
		{"a model that differs in case", `{"model":"a","Model":"b"}`, "", false},
		{"only a model in other case", `{"MODEL":"a"}`, "", false},
		{"a model that is not a string", `{"model":5}`, "", false},
		{"a null model", `{"model":null}`, "", false},
		{"an object model", `{"model":{"a":1}}`, "", false},
		{"not an object", `["model"]`, "", false},
		{"a string", `"model"`, "", false},
		{"empty", ``, "", false},
		{"a byte order mark", "\xef\xbb\xbf" + `{"model":"m"}`, "", false},
		{"cut short", `{"model":"m"`, "", false},
		{"cut inside the model", `{"model":"m`, "", false},
		{"cut before the colon", `{"model"`, "", false},
		{"a body that isn't JSON", `model=gpt-4o`, "", false},
		{"multipart", "--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nwhisper-1\r\n--b--", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := policy.ModelOf([]byte(tc.body))
			if got != tc.want || ok != tc.ok {
				t.Errorf("ModelOf(%s) = %q, %v; want %q, %v", tc.body, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestModelOfDoesNotCopyTheBody(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"` + strings.Repeat("x", 1<<20) + `"}]}`)
	allocs := testing.AllocsPerRun(10, func() { policy.ModelOf(body) })
	if allocs > 1 {
		t.Errorf("ModelOf made %v allocations on a 1 MiB body", allocs)
	}
}

func FuzzModelOf(f *testing.F) {
	for _, s := range []string{`{"model":"m"}`, `{"a":[1,{"model":"x"}],"model":"y"}`, `{"model":"m"}`, `{"model":"`, `[`, ``} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		model, ok := policy.ModelOf(body)
		if !ok && model != "" {
			t.Fatalf("ModelOf returned %q with ok false", model)
		}
	})
}
