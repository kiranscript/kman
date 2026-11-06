package adminapi_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirank55/kman/internal/adminapi"
)

var update = flag.Bool("update", false, "rewrite the committed OpenAPI document")

// committed is the generated OpenAPI document checked into the repo, the
// admin API reference (SPEC §16).
var committed = filepath.Join("..", "..", "docs", "admin-api", "openapi.json")

// TestTheOpenAPIDocumentIsCurrent fails when the committed document is
// stale. Regenerate it with
//
//	go test ./internal/adminapi -run OpenAPIDocumentIsCurrent -update
func TestTheOpenAPIDocumentIsCurrent(t *testing.T) {
	doc, err := adminapi.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(committed), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(committed, doc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may have turned the line endings into CRLF.
	if !bytes.Equal(doc, bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
		t.Errorf("%s is stale: run go test ./internal/adminapi -run OpenAPIDocumentIsCurrent -update", committed)
	}
}

func TestTheServerServesItsOpenAPIDocumentWithoutAToken(t *testing.T) {
	s := newServer(t)
	r := s.do(t, "", "GET", "/admin/v1/openapi.json", "")
	want, err := adminapi.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if r.status != http.StatusOK || !bytes.Equal(r.body, want) {
		t.Errorf("GET /admin/v1/openapi.json = %d, %d bytes; want 200 and the document", r.status, len(r.body))
	}
}

// TestTheOpenAPIDocumentDescribesTheTypes checks the document against what
// the API is known to look like, written out by hand.
func TestTheOpenAPIDocumentDescribesTheTypes(t *testing.T) {
	raw, err := adminapi.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Security    *[]any `json:"security"`
			Parameters  []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
				Schema   struct {
					Type string `json:"type"`
				} `json:"schema"`
			} `json:"parameters"`
			RequestBody *struct {
				Content map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
			Responses map[string]struct {
				Content map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Type       string                     `json:"type"`
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
			SecuritySchemes map[string]struct {
				Type   string `json:"type"`
				Scheme string `json:"scheme"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Errorf("openapi = %q, want 3.1.0", doc.OpenAPI)
	}
	ops := map[string]string{}
	for path, methods := range doc.Paths {
		for method, op := range methods {
			ops[method+" "+path] = op.OperationID
		}
	}
	for want, id := range map[string]string{
		"get /admin/v1/whoami":                      "whoami",
		"get /admin/v1/admin-tokens":                "listAdminTokens",
		"post /admin/v1/admin-tokens":               "createAdminToken",
		"post /admin/v1/admin-tokens/{name}/rotate": "rotateAdminToken",
		"post /admin/v1/admin-tokens/{name}/revoke": "revokeAdminToken",
		"get /admin/v1/settings":                    "getSettings",
		"patch /admin/v1/settings":                  "updateSettings",
		"get /admin/v1/audit":                       "listAudit",
		"get /admin/v1/prices":                      "listPrices",
		"get /admin/v1/prices/status":               "getPriceDataStatus",
		"get /admin/v1/openapi.json":                "openapi",
		"get /admin/v1/upstreams":                   "listUpstreams",
		"post /admin/v1/upstreams":                  "addUpstream",
		"get /admin/v1/upstreams/{ref}":             "getUpstream",
		"patch /admin/v1/upstreams/{ref}":           "updateUpstream",
		"delete /admin/v1/upstreams/{ref}":          "removeUpstream",
		"get /admin/v1/upstreams/{ref}/sync":        "planUpstreamSync",
		"post /admin/v1/upstreams/{ref}/sync":       "syncUpstream",
		"post /admin/v1/credentials":                "addCredential",
		"patch /admin/v1/credentials/{ref}":         "updateCredential",
		"delete /admin/v1/credentials/{ref}":        "removeCredential",
		"get /admin/v1/keys":                        "listKeys",
		"post /admin/v1/keys":                       "createKey",
		"get /admin/v1/keys/{id}":                   "getKey",
		"patch /admin/v1/keys/{id}":                 "updateKey",
		"put /admin/v1/price-overrides":             "setPriceOverride",
		"delete /admin/v1/price-overrides":          "removePriceOverride",
		"post /admin/v1/price-overrides/import":     "importPriceOverrides",
	} {
		if ops[want] != id {
			t.Errorf("%s has operationId %q, want %q", want, ops[want], id)
		}
	}
	if len(ops) != 28 {
		t.Errorf("the document has %d operations, want 28: %v", len(ops), ops)
	}

	// The settings an upstream takes reach the document, embedded as they are.
	addUpstream := doc.Components.Schemas["AddUpstreamRequest"]
	for _, field := range []string{"name", "preset", "host", "port", "tls", "path_allowlist", "auth", "sdk_env"} {
		if _, ok := addUpstream.Properties[field]; !ok {
			t.Errorf("AddUpstreamRequest has no %s: %v", field, addUpstream.Properties)
		}
	}
	// A credential's secret goes in, and never comes out.
	if _, ok := doc.Components.Schemas["AddCredentialRequest"].Properties["secret"]; !ok {
		t.Error("AddCredentialRequest has no secret")
	}
	for _, name := range []string{"Credential", "Upstream", "UpstreamList", "UpstreamSyncPlan"} {
		schema, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("no %s schema", name)
		}
		if _, ok := schema.Properties["secret"]; ok {
			t.Errorf("%s has a secret", name)
		}
	}
	// A key's secret comes out once, in the create response alone.
	if _, ok := doc.Components.Schemas["CreatedKey"].Properties["secret"]; !ok {
		t.Error("CreatedKey has no secret")
	}
	for _, name := range []string{"Key", "KeyList", "CreateKeyRequest", "KeyUpdate"} {
		schema, ok := doc.Components.Schemas[name]
		if !ok {
			t.Errorf("no %s schema", name)
		}
		if _, ok := schema.Properties["secret"]; ok {
			t.Errorf("%s has a secret", name)
		}
	}
	create := doc.Paths["/admin/v1/admin-tokens"]["post"]
	if ref := create.RequestBody.Content["application/json"].Schema.Ref; ref != "#/components/schemas/CreateAdminTokenRequest" {
		t.Errorf("createAdminToken's body is %q", ref)
	}
	if ref := create.Responses["201"].Content["application/json"].Schema.Ref; ref != "#/components/schemas/CreatedAdminToken" {
		t.Errorf("createAdminToken's 201 is %q", ref)
	}
	if ref := create.Responses["default"].Content["application/json"].Schema.Ref; ref != "#/components/schemas/ErrorResponse" {
		t.Errorf("createAdminToken's errors are %q", ref)
	}
	rotate := doc.Paths["/admin/v1/admin-tokens/{name}/rotate"]["post"]
	if len(rotate.Parameters) != 1 || rotate.Parameters[0].Name != "name" || rotate.Parameters[0].In != "path" || !rotate.Parameters[0].Required {
		t.Errorf("rotateAdminToken's parameters = %+v, want the name in the path", rotate.Parameters)
	}
	if rotate.RequestBody != nil {
		t.Error("rotateAdminToken takes a body")
	}
	audit := doc.Paths["/admin/v1/audit"]["get"]
	params := map[string]string{}
	for _, p := range audit.Parameters {
		params[p.In+" "+p.Name] = p.Schema.Type
	}
	if params["query after"] != "integer" || params["query limit"] != "integer" || params["query latest"] != "boolean" || len(params) != 3 {
		t.Errorf("listAudit's parameters = %v", params)
	}
	if sec := doc.Paths["/admin/v1/openapi.json"]["get"].Security; sec == nil || len(*sec) != 0 {
		t.Errorf("the document's own route has security %v, want none", sec)
	}
	if s := doc.Components.SecuritySchemes["adminToken"]; s.Type != "http" || s.Scheme != "bearer" {
		t.Errorf("security scheme = %+v, want http bearer", s)
	}

	token := doc.Components.Schemas["AdminToken"]
	if token.Type != "object" || len(token.Properties) != 4 {
		t.Errorf("AdminToken schema = %+v", token)
	}
	if want := []string{"created_at", "name"}; !equalSet(token.Required, want) {
		t.Errorf("AdminToken requires %v, want %v", token.Required, want)
	}
	if got := string(token.Properties["created_at"]); !bytes.Contains([]byte(got), []byte(`"date-time"`)) {
		t.Errorf("created_at is %s, want a date-time string", got)
	}
	if _, ok := doc.Components.Schemas["SettingsUpdate"].Properties["soft_budget_defaults"]; !ok {
		t.Error("SettingsUpdate lacks soft_budget_defaults")
	}
	if got := doc.Components.Schemas["SettingsUpdate"].Required; len(got) != 0 {
		t.Errorf("SettingsUpdate requires %v, want nothing", got)
	}
	// An override's prices are pointers so that absent isn't zero, and required
	// all the same; its optional prices are not.
	if got, want := doc.Components.Schemas["PriceOverrideSet"].Required, []string{"model", "input_micro", "output_micro", "max_output"}; !equalSet(got, want) {
		t.Errorf("PriceOverrideSet requires %v, want %v", got, want)
	}
	if _, ok := doc.Components.Schemas["PriceOverrideSet"].Properties["cached_micro"]; !ok {
		t.Error("PriceOverrideSet lacks cached_micro")
	}
	if _, ok := doc.Components.Schemas["PriceList"].Properties["overrides"]; !ok {
		t.Error("PriceList lacks overrides")
	}
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	for _, s := range b {
		if !m[s] {
			return false
		}
	}
	return true
}
