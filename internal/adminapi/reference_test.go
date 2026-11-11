package adminapi_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/adminapi"
)

// referencePath is the generated admin API reference (SPEC §16), a readable
// rendering of the OpenAPI document beside it.
var referencePath = filepath.Join("..", "..", "docs", "admin-api", "README.md")

// TestTheAdminAPIReferenceIsCurrent fails when docs/admin-api/README.md is
// stale. Regenerate it with
//
//	go test ./internal/adminapi -run AdminAPIReferenceIsCurrent -update
func TestTheAdminAPIReferenceIsCurrent(t *testing.T) {
	doc, err := adminapi.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	got := reference(t, doc)
	if *update {
		if err := os.WriteFile(referencePath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("%v: run go test ./internal/adminapi -run AdminAPIReferenceIsCurrent -update", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s is stale: run go test ./internal/adminapi -run AdminAPIReferenceIsCurrent -update", referencePath)
	}
}

// TestTheReferenceNamesEveryOperation holds the generator to the document: an
// operation or a schema it dropped would make the reference quietly short.
func TestTheReferenceNamesEveryOperation(t *testing.T) {
	doc, err := adminapi.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
		Components struct{ Schemas map[string]any } `json:"components"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	ref := reference(t, doc)
	n := 0
	for path, methods := range d.Paths {
		for method, op := range methods {
			n++
			if !strings.Contains(ref, "### `"+strings.ToUpper(method)+" "+path+"`") || !strings.Contains(ref, "`"+op.OperationID+"`") {
				t.Errorf("the reference lacks %s %s (%s)", strings.ToUpper(method), path, op.OperationID)
			}
		}
	}
	if n < 30 {
		t.Errorf("only %d operations: the document is broken", n)
	}
	for name := range d.Components.Schemas {
		if !strings.Contains(ref, "\n### `"+name+"`\n") {
			t.Errorf("the reference lacks the schema %s", name)
		}
	}
}

type node = map[string]any

func reference(t *testing.T, raw []byte) string {
	t.Helper()
	var d node
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	info := d["info"].(node)
	fmt.Fprintf(&b, "# %s\n\n", info["title"])
	b.WriteString("Generated from the Go types by `go test ./internal/adminapi -run AdminAPIReferenceIsCurrent -update`; do not edit by hand. " +
		"CI fails when it is stale. The same document in OpenAPI 3.1 form is [openapi.json](openapi.json), and a running kman serves it at `/admin/v1/openapi.json`, with no token.\n\n")
	fmt.Fprintf(&b, "%s\n\n", info["description"])
	b.WriteString("## Using it\n\n" +
		"- Every request, except the OpenAPI document, carries `Authorization: Bearer kma_…`, an [admin token](../cli.md#kman-admin-token-create). A cookie is never accepted.\n" +
		"- Request and response bodies are JSON. A request body with a field the API does not know is refused.\n" +
		"- An error is `{\"error\": {\"code\": …, \"message\": …}}` with one of the codes in [Error](#error), and the HTTP status that goes with it (400 `invalid`, 401 `unauthorized`, 404 `not_found`, 409 `conflict`, 500 `internal`).\n" +
		"- Every response carries `Cache-Control: no-store`.\n" +
		"- The listener is `127.0.0.1:8081` by default, or a `0600` Unix socket.\n\n")

	paths := d["paths"].(node)
	var keys []string
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	b.WriteString("## Operations\n\n| Operation | Summary |\n|---|---|\n")
	var ops []string
	for _, p := range keys {
		for _, m := range sortedKeys(paths[p].(node)) {
			op := paths[p].(node)[m].(node)
			ops = append(ops, p+"\x00"+m)
			fmt.Fprintf(&b, "| `%s %s` | %s |\n", strings.ToUpper(m), p, op["summary"])
		}
	}
	for _, key := range ops {
		p, m, _ := strings.Cut(key, "\x00")
		op := paths[p].(node)[m].(node)
		fmt.Fprintf(&b, "\n### `%s %s`\n\n%s. Operation id `%s`.\n", strings.ToUpper(m), p, strings.TrimSuffix(fmt.Sprint(op["summary"]), "."), op["operationId"])
		if sec, ok := op["security"].([]any); ok && len(sec) == 0 {
			b.WriteString("\nNeeds no admin token.\n")
		}
		if params, ok := op["parameters"].([]any); ok {
			b.WriteString("\n| Parameter | In | Type | Required | Description |\n|---|---|---|---|---|\n")
			for _, p := range params {
				p := p.(node)
				fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", p["name"], p["in"], typeOf(p["schema"]), yesNo(p["required"] == true), cell(p["description"]))
			}
		}
		if rb, ok := op["requestBody"].(node); ok {
			fmt.Fprintf(&b, "\nRequest body: %s.\n", typeOf(rb["content"].(node)["application/json"].(node)["schema"]))
		}
		resp := op["responses"].(node)
		for _, status := range sortedKeys(resp) {
			if status == "default" {
				continue
			}
			r := resp[status].(node)
			if c, ok := r["content"].(node); ok {
				fmt.Fprintf(&b, "\nResponse %s: %s.\n", status, typeOf(c["application/json"].(node)["schema"]))
			} else {
				fmt.Fprintf(&b, "\nResponse %s.\n", status)
			}
		}
	}

	b.WriteString("\n## Schemas\n")
	schemas := d["components"].(node)["schemas"].(node)
	for _, name := range sortedKeys(schemas) {
		s := schemas[name].(node)
		fmt.Fprintf(&b, "\n### `%s`\n\n", name)
		props, _ := s["properties"].(node)
		if len(props) == 0 {
			fmt.Fprintf(&b, "%s\n", typeOf(s))
			continue
		}
		required := map[string]bool{}
		for _, r := range asList(s["required"]) {
			required[fmt.Sprint(r)] = true
		}
		b.WriteString("| Field | Type | Required | Description |\n|---|---|---|---|\n")
		for _, f := range sortedKeys(props) {
			p := props[f].(node)
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", f, typeOf(p), yesNo(required[f]), cell(p["description"]))
		}
	}
	return b.String()
}

// typeOf renders a schema as a short type: a link for a named one.
func typeOf(v any) string {
	s, ok := v.(node)
	if !ok {
		return ""
	}
	if ref, ok := s["$ref"].(string); ok {
		name := ref[strings.LastIndex(ref, "/")+1:]
		return fmt.Sprintf("[%s](#%s)", name, strings.ToLower(name))
	}
	if all := asList(s["allOf"]); len(all) == 1 {
		return typeOf(all[0])
	}
	switch s["type"] {
	case "array":
		return "list of " + typeOf(s["items"])
	case "object":
		return "object"
	case "integer":
		return "integer"
	case nil:
		return ""
	}
	if e := asList(s["enum"]); len(e) > 0 {
		var vals []string
		for _, x := range e {
			vals = append(vals, fmt.Sprintf("`%v`", x))
		}
		return "one of " + strings.Join(vals, ", ")
	}
	return fmt.Sprint(s["type"])
}

func asList(v any) []any { l, _ := v.([]any); return l }

func sortedKeys(m node) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// cell makes a description fit one table cell.
func cell(v any) string {
	s, _ := v.(string)
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}
