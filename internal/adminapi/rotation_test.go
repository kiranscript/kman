package adminapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

func TestTheAPIRotatesAKeyByItsIdWithAGraceAndShowsTheNewSecretOnce(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	created := createKey(t, s, `{"name":"my-app","upstream":"openai"}`)

	r := s.do(t, s.owner, "POST", "/admin/v1/keys/"+created.Key.ID+"/rotate", `{"grace_seconds":3600}`)
	var got admin.RotatedKey
	if err := json.Unmarshal(r.body, &got); err != nil || r.status != http.StatusOK {
		t.Fatalf("%d %s: %v", r.status, r.body, err)
	}
	if got.Key.ID != created.Key.ID || got.Secret == created.Secret || !strings.HasPrefix(string(got.Secret), "kmn_"+created.Key.ID+"_") || got.PreviousValidUntil == nil {
		t.Errorf("rotated = %+v", got)
	}
	// With no body at all, it is a rotation with no grace.
	r = s.do(t, s.owner, "POST", "/admin/v1/keys/"+created.Key.ID+"/rotate", "")
	var again admin.RotatedKey
	if err := json.Unmarshal(r.body, &again); err != nil || r.status != http.StatusOK || again.PreviousValidUntil != nil {
		t.Errorf("%d %s: %v", r.status, r.body, err)
	}
	// The key shows no secret, before or after.
	if r := s.do(t, s.owner, "GET", "/admin/v1/keys/"+created.Key.ID, ""); strings.Contains(string(r.body), "kmn_") {
		t.Errorf("the key shows a secret: %s", r.body)
	}
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/admin/v1/keys/nokey123/rotate", ``, http.StatusNotFound},
		{"/admin/v1/keys/" + created.Key.ID + "/rotate", `{"grace_seconds":-5}`, http.StatusBadRequest},
		{"/admin/v1/keys/my-app/rotate", ``, http.StatusNotFound},
	} {
		if r := s.do(t, s.owner, "POST", tc.path, tc.body); r.status != tc.status {
			t.Errorf("POST %s %s: %d %s, want %d", tc.path, tc.body, r.status, r.body, tc.status)
		}
	}
}

func TestTheAPICompromisesACredentialAndListsTheKeysItRotated(t *testing.T) {
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	created := createKey(t, s, `{"name":"my-app","upstream":"openai"}`)

	r := s.do(t, s.owner, "POST", "/admin/v1/credentials/openai/compromise", `{"secret":"sk-test-replacement-0123456789"}`)
	var got admin.CompromisedCredential
	if err := json.Unmarshal(r.body, &got); err != nil || r.status != http.StatusOK {
		t.Fatalf("%d %s: %v", r.status, r.body, err)
	}
	if len(got.RotatedKeys) != 1 || got.RotatedKeys[0].Key.ID != created.Key.ID || got.RotatedKeys[0].Secret == created.Secret {
		t.Errorf("rotated keys = %+v", got.RotatedKeys)
	}
	if strings.Contains(string(r.body), "sk-test-replacement") {
		t.Errorf("the response echoes the credential: %s", r.body)
	}
	if r := s.do(t, s.owner, "POST", "/admin/v1/credentials/openai/compromise", `{"secret":""}`); r.status != http.StatusBadRequest {
		t.Errorf("an empty secret: %d %s", r.status, r.body)
	}
}
