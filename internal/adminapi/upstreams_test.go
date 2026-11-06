package adminapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAnUpstreamAndItsCredentialOverHTTP(t *testing.T) {
	s := newServer(t)
	r := s.do(t, s.owner, "POST", "/admin/v1/upstreams", `{"name":"openai","preset":"openai"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("add: %d %s", r.status, r.body)
	}
	var up struct {
		ID            string
		Name          string
		Preset        string
		PresetVersion int `json:"preset_version"`
		Host          string
		Paths         []string `json:"path_allowlist"`
		Auth          struct{ Style, Header string }
		SDKEnv        string `json:"sdk_env"`
		Credentials   []struct{ Name, Last4 string }
	}
	if err := json.Unmarshal(r.body, &up); err != nil {
		t.Fatal(err)
	}
	if up.Host != "api.openai.com" || up.PresetVersion != 1 || up.Auth.Header != "Authorization: Bearer {key}" || up.SDKEnv != "OPENAI_API_KEY" || len(up.Paths) == 0 {
		t.Errorf("add answered %s", r.body)
	}

	const secret = "sk-live-sentinel-0123456789abcdefWXYZ"
	r = s.do(t, s.owner, "POST", "/admin/v1/credentials", `{"name":"main","upstream":"openai","secret":"`+secret+`"}`)
	if r.status != http.StatusCreated || strings.Contains(string(r.body), secret) {
		t.Fatalf("add credential: %d %s", r.status, r.body)
	}

	for _, path := range []string{"/admin/v1/upstreams/openai", "/admin/v1/upstreams/" + up.ID} {
		r = s.do(t, s.owner, "GET", path, "")
		if err := json.Unmarshal(r.body, &up); r.status != http.StatusOK || err != nil {
			t.Fatalf("show %s: %d %s", path, r.status, r.body)
		}
		if len(up.Credentials) != 1 || up.Credentials[0].Name != "main" || up.Credentials[0].Last4 != "WXYZ" {
			t.Errorf("show %s answered %s", path, r.body)
		}
	}
	r = s.do(t, s.owner, "GET", "/admin/v1/upstreams", "")
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"name":"openai"`) {
		t.Errorf("list: %d %s", r.status, r.body)
	}

	r = s.do(t, s.owner, "PATCH", "/admin/v1/upstreams/openai", `{"host":"gw.example.com"}`)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"host":"gw.example.com"`) {
		t.Errorf("update: %d %s", r.status, r.body)
	}
	r = s.do(t, s.owner, "PATCH", "/admin/v1/credentials/main", `{"name":"primary"}`)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"name":"primary"`) {
		t.Errorf("update credential: %d %s", r.status, r.body)
	}
	r = s.do(t, s.owner, "GET", "/admin/v1/upstreams/openai/sync", "")
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"up_to_date":true`) {
		t.Errorf("plan sync: %d %s", r.status, r.body)
	}
	r = s.do(t, s.owner, "POST", "/admin/v1/upstreams/openai/sync", `{"to_version":1,"generation":1}`)
	if r.status != http.StatusConflict {
		t.Errorf("sync with nothing newer: %d %s", r.status, r.body)
	}
	r = s.do(t, s.owner, "DELETE", "/admin/v1/credentials/primary", "")
	if r.status != http.StatusOK {
		t.Errorf("remove credential: %d %s", r.status, r.body)
	}
	r = s.do(t, s.owner, "DELETE", "/admin/v1/upstreams/openai", "")
	if r.status != http.StatusOK {
		t.Errorf("remove: %d %s", r.status, r.body)
	}
	r = s.do(t, s.owner, "GET", "/admin/v1/upstreams/openai", "")
	if r.status != http.StatusNotFound {
		t.Errorf("show after remove: %d %s", r.status, r.body)
	}
}

func TestACustomUpstreamOverHTTP(t *testing.T) {
	s := newServer(t)
	r := s.do(t, s.owner, "POST", "/admin/v1/upstreams",
		`{"name":"acme","host":"api.acme.test","path_allowlist":["POST /v1/things"],"auth":{"style":"basic","basic_username":"api"}}`)
	if r.status != http.StatusCreated || !strings.Contains(string(r.body), `"basic_username":"api"`) || !strings.Contains(string(r.body), `"tls":true`) {
		t.Fatalf("add: %d %s", r.status, r.body)
	}
}

func TestUpstreamErrorsMapToStatuses(t *testing.T) {
	s := newServer(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/admin/v1/upstreams", `{"preset":"nope"}`, http.StatusBadRequest},
		{"POST", "/admin/v1/upstreams", `{"name":"x","unknown_field":1}`, http.StatusBadRequest},
		{"GET", "/admin/v1/upstreams/nope", "", http.StatusNotFound},
		{"PATCH", "/admin/v1/upstreams/nope", `{"host":"a.example.com"}`, http.StatusNotFound},
		{"DELETE", "/admin/v1/upstreams/nope", "", http.StatusNotFound},
		{"GET", "/admin/v1/upstreams/nope/sync", "", http.StatusNotFound},
		{"POST", "/admin/v1/credentials", `{"name":"x","upstream":"nope","secret":"s"}`, http.StatusNotFound},
		{"PATCH", "/admin/v1/credentials/nope", `{"name":"y"}`, http.StatusNotFound},
		{"DELETE", "/admin/v1/credentials/nope", "", http.StatusNotFound},
	} {
		if r := s.do(t, s.owner, tc.method, tc.path, tc.body); r.status != tc.status {
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, r.status, r.body, tc.status)
		}
	}
	if r := s.do(t, "", "GET", "/admin/v1/upstreams", ""); r.status != http.StatusUnauthorized {
		t.Errorf("no token = %d", r.status)
	}
}

func TestARefusedSecretIsNeitherAnsweredNorLogged(t *testing.T) {
	s := newServer(t)
	const secret = "sk-live-sentinel-0123456789abcdefWXYZ"
	s.do(t, s.owner, "POST", "/admin/v1/upstreams", `{"name":"twilio","preset":"twilio"}`)
	for _, body := range []string{
		`{"name":"x","upstream":"twilio","secret":"` + secret + `"}`,
		`{"name":"Bad Name","upstream":"twilio","secret":"` + secret + `"}`,
		`{"name":"x","upstream":"twilio","secret":"` + secret + `","extra":"` + secret + `"}`,
		`{"name":"x","upstream":"twilio","secret":["` + secret + `"]}`,
		`{"name":"x","upstream":"twilio","secret":"` + secret,
	} {
		r := s.do(t, s.owner, "POST", "/admin/v1/credentials", body)
		if r.status < 400 || strings.Contains(string(r.body), secret) {
			t.Errorf("%s answered %d %s", body, r.status, r.body)
		}
	}
	if strings.Contains(s.logs.String(), secret) {
		t.Errorf("logged the secret: %s", s.logs)
	}
}
