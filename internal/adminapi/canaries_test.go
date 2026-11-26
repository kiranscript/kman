package adminapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kiranscript/kman/internal/admin"
)

func TestTheAPIMakesListsShowsUpdatesAndResetsACanary(t *testing.T) {
	s := newServer(t)
	r := s.do(t, s.owner, "POST", "/admin/v1/canaries",
		`{"name":"decoy","planted":"the deploy repo's .env","format":"shell","tags":["prod"],"ignore":["192.0.2.0/24"]}`)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /canaries: %d %s", r.status, r.body)
	}
	var created admin.CreatedCanary
	if err := json.Unmarshal(r.body, &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(created.Secret), "kmn_"+created.Canary.ID+"_") || created.Format != "shell" ||
		created.Snippet != "export OPENAI_API_KEY="+string(created.Secret)+"\n" || created.Canary.State != "armed" {
		t.Errorf("created = %+v", created)
	}
	id := created.Canary.ID

	// Neither the list, a read, nor the key's own routes give the secret again.
	for _, r := range []response{
		s.do(t, s.owner, "GET", "/admin/v1/canaries", ""),
		s.do(t, s.owner, "GET", "/admin/v1/canaries/"+id, ""),
		s.do(t, s.owner, "GET", "/admin/v1/keys/"+id, ""),
		s.do(t, s.owner, "GET", "/admin/v1/keys?name=decoy", ""),
	} {
		if r.status != http.StatusOK || strings.Contains(string(r.body), string(created.Secret)) {
			t.Errorf("%d %s", r.status, r.body)
		}
	}
	var list admin.CanaryList
	if err := json.Unmarshal(s.do(t, s.owner, "GET", "/admin/v1/canaries", "").body, &list); err != nil || len(list.Canaries) != 1 || list.Canaries[0].ID != id {
		t.Errorf("list = %+v, %v", list, err)
	}
	// A canary is not in the list of keys.
	var keys admin.KeyList
	if err := json.Unmarshal(s.do(t, s.owner, "GET", "/admin/v1/keys", "").body, &keys); err != nil || len(keys.Keys) != 0 {
		t.Errorf("keys = %+v, %v; want none: a canary is not a key", keys, err)
	}

	r = s.do(t, s.owner, "PATCH", "/admin/v1/canaries/"+id, `{"planted":"the wiki","ignore":[]}`)
	var updated admin.Canary
	if err := json.Unmarshal(r.body, &updated); err != nil || r.status != http.StatusOK || updated.Planted != "the wiki" || len(updated.Ignore) != 0 {
		t.Errorf("PATCH: %d %s, %v", r.status, r.body, err)
	}
	r = s.do(t, s.owner, "POST", "/admin/v1/canaries/"+id+"/reset", "")
	var reset admin.Canary
	if err := json.Unmarshal(r.body, &reset); err != nil || r.status != http.StatusOK || reset.State != "armed" {
		t.Errorf("reset: %d %s, %v", r.status, r.body, err)
	}
}

func TestTheAPIRefusesWhatMakesNoCanary(t *testing.T) {
	s := newServer(t)
	for _, body := range []string{
		`{}`,
		`{"planted":"x","format":"yaml"}`,
		`{"planted":"x","ignore":["0.0.0.0/0"]}`,
		`{"planted":"x","unknown":1}`,
	} {
		if r := s.do(t, s.owner, "POST", "/admin/v1/canaries", body); r.status != http.StatusBadRequest {
			t.Errorf("POST /canaries %s: %d %s", body, r.status, r.body)
		}
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/v1/canaries/nokey123"}, {"POST", "/admin/v1/canaries/nokey123/reset"},
	} {
		if r := s.do(t, s.owner, c.method, c.path, ""); r.status != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	// It needs an admin token like everything else.
	if r := s.do(t, "", "GET", "/admin/v1/canaries", ""); r.status != http.StatusUnauthorized {
		t.Errorf("GET /canaries with no token: %d", r.status)
	}
}
