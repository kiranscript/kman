package policy_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/policy"
)

// BenchmarkEvaluate is the policy's cost of one request, with the model read out
// of a 4 KiB body: what the gateway adds for a key with allowlists.
func BenchmarkEvaluate(b *testing.B) {
	paths, err := policy.CompilePaths([]string{"POST /v1/chat/completions", "GET /v1/models/*", "* /v1/files/**"})
	if err != nil {
		b.Fatal(err)
	}
	c, err := policy.Compile(1, `{"allowed_models":["gpt-4o","claude-*"]}`)
	if err != nil {
		b.Fatal(err)
	}
	body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("q", 4096) + `"}],"model":"claude-sonnet-4"}`)
	b.ReportAllocs()
	for b.Loop() {
		model, ok := policy.ModelOf(body)
		if policy.Evaluate(policy.Facts{Method: "POST", Path: "/v1/chat/completions", Model: model, HasModel: ok, Upstream: paths}, c) != policy.Allow {
			b.Fatal("refused")
		}
	}
}
