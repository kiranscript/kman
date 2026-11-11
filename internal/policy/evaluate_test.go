package policy_test

import (
	"testing"

	"github.com/kirank55/kman/internal/policy"
)

func compile(t *testing.T, raw string) *policy.Compiled {
	t.Helper()
	version, normalized, err := policy.Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("Normalize(%s) = %v", raw, err)
	}
	c, err := policy.Compile(version, string(normalized))
	if err != nil {
		t.Fatalf("Compile(%s) = %v", raw, err)
	}
	return c
}

func rules(t *testing.T, r ...string) *policy.Paths {
	t.Helper()
	p, err := policy.CompilePaths(r)
	if err != nil {
		t.Fatalf("CompilePaths(%q) = %v", r, err)
	}
	return p
}

const (
	pathNot  = policy.PathNotAllowed
	modelNot = policy.ModelNotAllowed
	unmeter  = policy.Unmetered
)

func TestEvaluatePathAllowlists(t *testing.T) {
	upstream := rules(t, "POST /v1/chat/completions", "GET /v1/models", "GET /v1/models/*", "* /v1/files/**")
	for _, tc := range []struct {
		name   string
		policy string
		method string
		path   string
		want   policy.Decision
	}{
		{"an exact rule", `{}`, "POST", "/v1/chat/completions", policy.Allow},
		{"the wrong method", `{}`, "GET", "/v1/chat/completions", pathNot},
		{"a method is exact", `{}`, "post", "/v1/chat/completions", pathNot},
		{"an unlisted path", `{}`, "POST", "/v1/batches", pathNot},
		{"a longer path is not the rule's path", `{}`, "POST", "/v1/chat/completions/extra", pathNot},
		{"a shorter path is not the rule's path", `{}`, "POST", "/v1/chat", pathNot},
		{"a trailing slash is another path", `{}`, "POST", "/v1/chat/completions/", pathNot},
		{"* is one segment", `{}`, "GET", "/v1/models/gpt-4o", policy.Allow},
		{"* is not two segments", `{}`, "GET", "/v1/models/a/b", pathNot},
		{"* is a segment, not none", `{}`, "GET", "/v1/models/", policy.Allow},
		{"** is the rest", `{}`, "DELETE", "/v1/files/a/b/c", policy.Allow},
		{"** is one segment or more", `{}`, "PUT", "/v1/files/x", policy.Allow},
		{"** is not zero segments", `{}`, "PUT", "/v1/files", pathNot},
		{"a * method is any method", `{}`, "PATCH", "/v1/files/x", policy.Allow},
		{"rules are case sensitive", `{}`, "POST", "/V1/chat/completions", pathNot},

		// A traversal or an odd encoding must not reach the upstream, which may
		// read the path differently from the matcher.
		{"dot-dot out of an allowed path", `{}`, "GET", "/v1/files/../../admin", pathNot},
		{"dot-dot", `{}`, "GET", "/v1/files/..", pathNot},
		{"dot", `{}`, "GET", "/v1/files/.", pathNot},
		{"an encoded dot-dot", `{}`, "GET", "/v1/files/%2e%2e/x", pathNot},
		{"a half-encoded dot-dot", `{}`, "GET", "/v1/files/.%2E/x", pathNot},
		{"an encoded slash", `{}`, "GET", "/v1/files/a%2Fb", pathNot},
		{"an encoded backslash", `{}`, "GET", "/v1/files/a%5cb", pathNot},
		{"a backslash", `{}`, "GET", `/v1/files/a\b`, pathNot},
		{"an encoded NUL", `{}`, "GET", "/v1/files/a%00b", pathNot},
		{"a doubled slash", `{}`, "GET", "//v1/models", pathNot},
		{"a doubled slash inside", `{}`, "GET", "/v1//models", pathNot},
		{"an encoded letter still matches", `{}`, "POST", "/v1/%63hat/completions", policy.Allow},
		{"a path without a root", `{}`, "GET", "v1/models", pathNot},
		{"no path at all", `{}`, "GET", "", pathNot},
		{"the star form", `{}`, "OPTIONS", "*", pathNot},

		// A key's own list replaces the upstream's, so it can narrow it.
		{"a key list narrows", `{"allowed_paths":["POST /v1/chat/completions"]}`, "GET", "/v1/models", pathNot},
		{"a key list keeps what it names", `{"allowed_paths":["POST /v1/chat/completions"]}`, "POST", "/v1/chat/completions", policy.Allow},
		// A key with no model allowlist may add what the upstream's list leaves out.
		{"a key list may widen", `{"allowed_paths":["POST /v1/batches"]}`, "POST", "/v1/batches", policy.Allow},
		// With one it may only narrow: a model allowlist doesn't hold on a route
		// kman can't read a model from.
		{"a model allowlist keeps the key inside the upstream's list", `{"allowed_models":["gpt-4o"],"allowed_paths":["POST /v1/batches"]}`, "POST", "/v1/batches", pathNot},
		{"narrowing with a model allowlist", `{"allowed_models":["gpt-4o"],"allowed_paths":["POST /v1/chat/completions"]}`, "GET", "/v1/models", pathNot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.Evaluate(policy.Facts{Method: tc.method, Path: tc.path, Model: "gpt-4o", HasModel: true, Upstream: upstream}, compile(t, tc.policy))
			if got != tc.want {
				t.Errorf("Evaluate(%s %s) under %s = %v, want %v", tc.method, tc.path, tc.policy, got, tc.want)
			}
		})
	}
}

func TestEvaluateModelAllowlists(t *testing.T) {
	upstream := rules(t, "* /**")
	for _, tc := range []struct {
		name     string
		policy   string
		model    string
		hasModel bool
		want     policy.Decision
	}{
		{"no allowlist allows any model", `{}`, "anything", true, policy.Allow},
		{"no allowlist allows no model", `{}`, "", false, policy.Allow},
		{"a listed model", `{"allowed_models":["gpt-4o","gpt-4o-mini"]}`, "gpt-4o-mini", true, policy.Allow},
		{"an unlisted model", `{"allowed_models":["gpt-4o"]}`, "gpt-4o-mini", true, modelNot},
		{"models match exactly", `{"allowed_models":["gpt-4o"]}`, "GPT-4o", true, modelNot},
		{"not by prefix", `{"allowed_models":["gpt-4o"]}`, "gpt-4o-2024-08-06", true, modelNot},
		{"a trailing * is a prefix", `{"allowed_models":["gpt-4o*"]}`, "gpt-4o-2024-08-06", true, policy.Allow},
		{"a prefix rule matches the bare prefix", `{"allowed_models":["gpt-4o*"]}`, "gpt-4o", true, policy.Allow},
		{"a prefix rule is not a contains rule", `{"allowed_models":["gpt-4o*"]}`, "x-gpt-4o", true, modelNot},
		// A request with no readable model is not let through a model allowlist:
		// it could name any model upstream. That is the unmetered rule.
		{"no model on a key with an allowlist", `{"allowed_models":["gpt-4o"]}`, "", false, unmeter},
		{"an empty model is not a model", `{"allowed_models":["gpt-4o"]}`, "", true, modelNot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.Evaluate(policy.Facts{Method: "POST", Path: "/v1/x", Model: tc.model, HasModel: tc.hasModel, Upstream: upstream}, compile(t, tc.policy))
			if got != tc.want {
				t.Errorf("Evaluate(model %q) under %s = %v, want %v", tc.model, tc.policy, got, tc.want)
			}
		})
	}
}

func TestPathChecksComeBeforeModelChecks(t *testing.T) {
	c := compile(t, `{"allowed_models":["gpt-4o"]}`)
	got := policy.Evaluate(policy.Facts{Method: "GET", Path: "/nope", Model: "other", HasModel: true, Upstream: rules(t, "POST /v1/x")}, c)
	if got != pathNot {
		t.Errorf("got %v, want the path refusal first", got)
	}
}

func TestEvaluateWithNoUpstreamRulesAllowsNoPath(t *testing.T) {
	got := policy.Evaluate(policy.Facts{Method: "GET", Path: "/v1/models"}, compile(t, `{}`))
	if got != pathNot {
		t.Errorf("got %v: a request that no rule names is not allowed", got)
	}
}

func TestEvaluateIsRepeatable(t *testing.T) {
	c := compile(t, `{"allowed_models":["gpt-4o"],"rpm_limit":5}`)
	f := policy.Facts{Method: "POST", Path: "/v1/x", Model: "gpt-4o", HasModel: true, Upstream: rules(t, "* /**")}
	for i := 0; i < 3; i++ {
		if got := policy.Evaluate(f, c); got != policy.Allow {
			t.Fatalf("call %d = %v", i, got)
		}
	}
}

func TestACompiledPolicyReportsItsRPMLimit(t *testing.T) {
	if got := compile(t, `{"rpm_limit":30}`).RPM(); got != 30 {
		t.Errorf("RPM() = %d", got)
	}
	if got := compile(t, `{}`).RPM(); got != 0 {
		t.Errorf("RPM() = %d, want 0 for no limit", got)
	}
}

// A stored policy that can't be read must not let a request through.
func TestAPolicyThatCannotBeReadAllowsNothing(t *testing.T) {
	for _, tc := range []struct {
		version int
		raw     string
	}{{99, `{}`}, {1, `{"nope":1}`}, {1, `not json`}} {
		c, err := policy.Compile(tc.version, tc.raw)
		if err == nil {
			t.Errorf("Compile(%d, %s) = nil error", tc.version, tc.raw)
		}
		if c == nil {
			t.Fatalf("Compile(%d, %s) returned no policy to hold the key to", tc.version, tc.raw)
		}
		got := policy.Evaluate(policy.Facts{Method: "POST", Path: "/v1/x", Model: "m", HasModel: true, Upstream: rules(t, "* /**")}, c)
		if got == policy.Allow {
			t.Errorf("Compile(%d, %s) allowed a request", tc.version, tc.raw)
		}
	}
}

func TestANilPolicyAllowsNothing(t *testing.T) {
	got := policy.Evaluate(policy.Facts{Method: "POST", Path: "/v1/x", Upstream: rules(t, "* /**")}, nil)
	if got == policy.Allow {
		t.Error("a nil policy allowed a request")
	}
}

func TestNormalizeChecksTheFieldsItKnows(t *testing.T) {
	good := map[string]string{
		`{"allowed_paths":["POST /v1/x"]}`:                       `{"allowed_paths":["POST /v1/x"]}`,
		`{"allowed_models":["a","b*"],"rpm_limit":60}`:           `{"allowed_models":["a","b*"],"rpm_limit":60}`,
		`{"allowed_paths":[],"allowed_models":[],"rpm_limit":0}`: `{}`,
	}
	for raw, want := range good {
		_, got, err := policy.Normalize([]byte(raw))
		if err != nil || string(got) != want {
			t.Errorf("Normalize(%s) = %s, %v; want %s", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		`{"allowed_paths":["/v1/x"]}`, // no method
		`{"allowed_paths":["POST /v1/../x"]}`,
		`{"allowed_paths":["POST /v1/a*b"]}`,
		`{"allowed_paths":["POST /**/x"]}`,
		`{"allowed_paths":["POST /v1/x","POST /v1/x"]}`,
		`{"allowed_paths":"POST /v1/x"}`,
		`{"allowed_models":[""]}`,
		`{"allowed_models":["a","a"]}`,
		`{"allowed_models":["a*b"]}`,
		`{"allowed_models":["*"]}`, // use no allowlist
		`{"allowed_models":["a b"]}`,
		`{"rpm_limit":-1}`,
		`{"rpm_limit":1000001}`,
		`{"rpm_limit":1.5}`,
		`{"rpm_limit":"5"}`,
	} {
		if _, got, err := policy.Normalize([]byte(raw)); err == nil {
			t.Errorf("Normalize(%s) = %s, want an error", raw, got)
		}
	}
}

func TestCompilePathsRefusesABadRule(t *testing.T) {
	for _, r := range []string{"", "/v1", "post /v1", "POST v1", "POST /a/../b", "POST /a?x", "POST /a**", "POST /**/b"} {
		if _, err := policy.CompilePaths([]string{r}); err == nil {
			t.Errorf("CompilePaths(%q) = nil error", r)
		}
	}
}
