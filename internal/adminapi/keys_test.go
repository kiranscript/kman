package adminapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

// withUpstreamAndCredential adds an upstream with one credential.
func withUpstreamAndCredential(t *testing.T, s *server) {
	t.Helper()
	r := s.do(t, s.owner, "POST", "/admin/v1/upstreams", `{"preset":"openai","credential":{"secret":"sk-test-0123456789abcdefghij"}}`)
	if r.status != http.StatusCreated {
		t.Fatalf("add upstream: %d %s", r.status, r.body)
	}
}

func createKey(t *testing.T, s *server, body string) admin.CreatedKey {
	t.Helper()
	r := s.do(t, s.owner, "POST", "/admin/v1/keys", body)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /keys %s: %d %s", body, r.status, r.body)
	}
	var out admin.CreatedKey
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTheAPICreatesAKeyAndAddressesItByItsId(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	created := createKey(t, s, `{"name":"my-app","upstream":"openai","tags":["prod"]}`)
	if !strings.HasPrefix(string(created.Secret), "kmn_"+created.Key.ID+"_") {
		t.Errorf("secret does not hold the key id %s", created.Key.ID)
	}

	for _, r := range []response{
		s.do(t, s.owner, "GET", "/admin/v1/keys/"+created.Key.ID, ""),
		s.do(t, s.owner, "PATCH", "/admin/v1/keys/"+created.Key.ID, `{"note":"hello"}`),
	} {
		var k admin.Key
		if err := json.Unmarshal(r.body, &k); err != nil || r.status != http.StatusOK || k.ID != created.Key.ID {
			t.Errorf("%d %s: %v", r.status, r.body, err)
		}
	}
	// The API uses key ids: a name addresses nothing.
	for _, r := range []response{
		s.do(t, s.owner, "GET", "/admin/v1/keys/my-app", ""),
		s.do(t, s.owner, "PATCH", "/admin/v1/keys/my-app", `{"note":"x"}`),
	} {
		if r.status != http.StatusNotFound {
			t.Errorf("addressing a key by name: %d %s", r.status, r.body)
		}
	}
	r := s.do(t, s.owner, "GET", "/admin/v1/keys?name=my-app", "")
	var list admin.KeyList
	if err := json.Unmarshal(r.body, &list); err != nil || len(list.Keys) != 1 || list.Keys[0].ID != created.Key.ID {
		t.Errorf("listing by name: %s, %v", r.body, err)
	}
}

func TestTheAPIRefusesNeverAndAnythingElseThatIsNotASpan(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	for _, body := range []string{
		`{"upstream":"openai","ttl_seconds":"never"}`,
		`{"upstream":"openai","ttl_seconds":0}`,
		`{"upstream":"openai","ttl_seconds":-1}`,
		`{"upstream":"openai","ttl_seconds":31536001}`,
		`{"upstream":"openai","expires_at":"never"}`,
	} {
		if r := s.do(t, s.owner, "POST", "/admin/v1/keys", body); r.status != http.StatusBadRequest {
			t.Errorf("POST /keys %s: %d %s", body, r.status, r.body)
		}
	}
}

func TestThePolicyTravelsAsJSONThroughTheAPI(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	created := createKey(t, s, `{"upstream":"openai","policy":{ }}`)
	r := s.do(t, s.owner, "GET", "/admin/v1/keys/"+created.Key.ID, "")
	if !strings.Contains(string(r.body), `"policy":{}`) || !strings.Contains(string(r.body), `"policy_version":1`) {
		t.Errorf("GET: %s", r.body)
	}
	for _, body := range []string{`{"policy":null}`, `{"policy":[]}`, `{"policy":{"no_such_field":1}}`} {
		if r := s.do(t, s.owner, "PATCH", "/admin/v1/keys/"+created.Key.ID, body); r.status != http.StatusBadRequest {
			t.Errorf("PATCH %s: %d %s", body, r.status, r.body)
		}
	}
}

// Only the create response holds the secret, whatever else is asked, and the
// server logs nothing of it.
func TestTheSecretAppearsOnlyInTheCreateResponseOfTheAPI(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	created := createKey(t, s, `{"name":"my-app","upstream":"openai"}`)
	secret := string(created.Secret)
	for _, r := range []response{
		s.do(t, s.owner, "GET", "/admin/v1/keys", ""),
		s.do(t, s.owner, "GET", "/admin/v1/keys/"+created.Key.ID, ""),
		s.do(t, s.owner, "GET", "/admin/v1/keys?name=my-app", ""),
		s.do(t, s.owner, "PATCH", "/admin/v1/keys/"+created.Key.ID, `{"note":"changed"}`),
		s.do(t, s.owner, "GET", "/admin/v1/audit", ""),
	} {
		if r.status != http.StatusOK {
			t.Errorf("%d %s", r.status, r.body)
		}
		if strings.Contains(string(r.body), secret) {
			t.Errorf("a response holds the secret: %s", r.body)
		}
	}
	if logs := s.logs.String(); strings.Contains(logs, secret) {
		t.Errorf("the logs hold the secret: %s", logs)
	}
}

func TestTheAPIDisablesEnablesAndRevokesAKeyByItsId(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	id := createKey(t, s, `{"name":"my-app","upstream":"openai"}`).Key.ID
	post := func(action string) (int, admin.Key) {
		t.Helper()
		r := s.do(t, s.owner, "POST", "/admin/v1/keys/"+id+"/"+action, "")
		var k admin.Key
		if r.status == http.StatusOK {
			if err := json.Unmarshal(r.body, &k); err != nil {
				t.Fatalf("%s: %s: %v", action, r.body, err)
			}
		}
		return r.status, k
	}
	for _, step := range []struct{ action, status string }{{"disable", "disabled"}, {"enable", "active"}, {"disable", "disabled"}, {"revoke", "revoked"}} {
		if code, k := post(step.action); code != http.StatusOK || k.Status != step.status || k.ID != id {
			t.Errorf("%s = %d %+v, want status %s", step.action, code, k, step.status)
		}
	}
	if code, _ := post("enable"); code != http.StatusConflict {
		t.Errorf("enabling a revoked key = %d, want 409", code)
	}
	if r := s.do(t, s.owner, "POST", "/admin/v1/keys/my-app/revoke", ""); r.status != http.StatusNotFound {
		t.Errorf("revoking by name = %d %s, want 404", r.status, r.body)
	}
	if r := s.do(t, "", "POST", "/admin/v1/keys/"+id+"/revoke", ""); r.status != http.StatusUnauthorized {
		t.Errorf("revoking with no token = %d, want 401", r.status)
	}
}

func TestTheAPIListsTheExposedKeysWithTheirFixes(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	open := createKey(t, s, `{"name":"my-app","upstream":"openai"}`)
	createKey(t, s, `{"name":"locked","upstream":"openai","policy":{"allowed_cidrs":["203.0.113.0/24"]}}`)

	if r := s.do(t, "", "GET", "/admin/v1/exposure", ""); r.status != http.StatusUnauthorized {
		t.Errorf("GET /exposure with no token: %d, want 401", r.status)
	}
	r := s.do(t, s.owner, "GET", "/admin/v1/exposure", "")
	var got admin.Exposure
	if err := json.Unmarshal(r.body, &got); err != nil || r.status != http.StatusOK {
		t.Fatalf("GET /exposure: %d %s %v", r.status, r.body, err)
	}
	if got.KeysChecked != 2 || len(got.Findings) != 1 || got.Findings[0].KeyID != open.Key.ID ||
		got.Findings[0].Rule != admin.ExposureOpenToEveryAddress || !strings.HasPrefix(got.Findings[0].Fix, "kman key update my-app --policy") {
		t.Errorf("exposure = %s", r.body)
	}
	if strings.Contains(string(r.body), string(open.Secret)) {
		t.Error("the exposure report holds a key's secret")
	}
}
