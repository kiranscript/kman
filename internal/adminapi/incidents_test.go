package adminapi_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/admin"
	"github.com/kiranscript/kman/internal/detect"
	"github.com/kiranscript/kman/internal/store"
)

// withIncident is a server with a key and an open incident for it, incident 1.
func withIncident(t *testing.T) (*server, admin.CreatedKey) {
	t.Helper()
	s := newServer(t)
	withUpstreamAndCredential(t, s)
	k := createKey(t, s, `{"name":"my-app","upstream":"openai"}`)
	e := detect.New(detect.Options{Store: s.store, Now: func() time.Time { return t0 }})
	_, err := e.RaiseNow(context.Background(), detect.Detection{KeyID: k.Key.ID, Rule: "new_country", Severity: store.SeverityHigh,
		Evidence: store.Evidence{Address: "203.0.113.7", Country: "DE", ASN: 3320, Client: "curl/8"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, k
}

func TestTheAPIListsAndShowsIncidentsAndWithholdsAddresses(t *testing.T) {
	s, k := withIncident(t)
	var list admin.IncidentList
	s.do(t, s.owner, "GET", "/admin/v1/incidents?open=true", "").decode(t, 200, &list)
	if len(list.Incidents) != 1 || list.Incidents[0].ID != 1 || list.Incidents[0].KeyID != k.Key.ID || list.Incidents[0].KeyName != "my-app" || list.Incidents[0].Severity != "high" {
		t.Fatalf("list = %+v", list)
	}
	r := s.do(t, s.owner, "GET", "/admin/v1/incidents/1", "")
	var d admin.IncidentDetail
	r.decode(t, 200, &d)
	if strings.Contains(string(r.body), "203.0.113") || !d.AddressesWithheld || len(d.Detections) != 1 || d.Detections[0].Evidence.Country != "DE" {
		t.Errorf("the detail gave out an address, or lacks what it should: %s", r.body)
	}
	on := true
	if _, err := s.svc.UpdateSettings(admin.WithCaller(context.Background(), admin.Caller{Actor: "owner"}), admin.SettingsUpdate{ExposeClientAddresses: &on}); err != nil {
		t.Fatal(err)
	}
	r = s.do(t, s.owner, "GET", "/admin/v1/incidents/1", "")
	if !strings.Contains(string(r.body), "203.0.113.7") {
		t.Errorf("with the setting on the detail has no address: %s", r.body)
	}
	for _, bad := range []string{"/admin/v1/incidents/abc", "/admin/v1/incidents?limit=x", "/admin/v1/incidents?limit=9999"} {
		if r := s.do(t, s.owner, "GET", bad, ""); r.status != http.StatusBadRequest {
			t.Errorf("GET %s: %d", bad, r.status)
		}
	}
	if r := s.do(t, s.owner, "GET", "/admin/v1/incidents/99", ""); r.status != http.StatusNotFound {
		t.Errorf("GET /incidents/99: %d", r.status)
	}
	if r := s.do(t, "", "GET", "/admin/v1/incidents", ""); r.status != http.StatusUnauthorized {
		t.Errorf("GET /incidents with no token: %d", r.status)
	}
}

func TestTheAPIAcknowledgesAndResolvesAnIncident(t *testing.T) {
	s, _ := withIncident(t)
	var inc admin.Incident
	s.do(t, s.owner, "POST", "/admin/v1/incidents/1/acknowledge", "").decode(t, 200, &inc)
	if inc.State != "acknowledged" || inc.AcknowledgedBy != "owner" {
		t.Errorf("ack = %+v", inc)
	}
	s.do(t, s.owner, "POST", "/admin/v1/incidents/1/resolve", `{"as":"false_positive"}`).decode(t, 200, &inc)
	if inc.State != "resolved" || inc.Resolution != "false_positive" {
		t.Errorf("resolve = %+v", inc)
	}
	if r := s.do(t, s.owner, "POST", "/admin/v1/incidents/1/resolve", `{"as":"confirmed"}`); r.status != http.StatusConflict {
		t.Errorf("resolving another way: %d %s", r.status, r.body)
	}
	if r := s.do(t, s.owner, "POST", "/admin/v1/incidents/1/resolve", `{"as":"expired"}`); r.status != http.StatusBadRequest {
		t.Errorf("resolving as expired: %d", r.status)
	}
}

func TestTheAPISaysThisWasMeAndManagesKnownSources(t *testing.T) {
	s, _ := withIncident(t)
	var res admin.ThisWasMeResult
	s.do(t, s.owner, "POST", "/admin/v1/incidents/1/this-was-me", `{"add_known_sources":true,"install_wide":true,"kinds":["country"]}`).decode(t, 200, &res)
	if res.Incident.Resolution != "false_positive" || len(res.Added) != 1 || res.Added[0].Value != "DE" || res.Added[0].Scope != "" {
		t.Fatalf("this-was-me = %+v", res)
	}
	var added admin.KnownSource
	s.do(t, s.owner, "POST", "/admin/v1/known-sources", `{"kind":"cidr","value":"203.0.113.0/24","label":"CI"}`).decode(t, 201, &added)
	var list admin.KnownSourceList
	s.do(t, s.owner, "GET", "/admin/v1/known-sources", "").decode(t, 200, &list)
	if len(list.KnownSources) != 2 {
		t.Fatalf("list = %+v", list)
	}
	for _, bad := range []string{`{"kind":"cidr","value":"0.0.0.0/0","label":"x"}`, `{"kind":"country","value":"DE"}`, `{"kind":"country","value":"DE","label":"x","nope":1}`} {
		if r := s.do(t, s.owner, "POST", "/admin/v1/known-sources", bad); r.status != http.StatusBadRequest {
			t.Errorf("POST /known-sources %s: %d %s", bad, r.status, r.body)
		}
	}
	if r := s.do(t, s.owner, "POST", "/admin/v1/known-sources", `{"kind":"cidr","value":"203.0.113.0/24","label":"again"}`); r.status != http.StatusConflict {
		t.Errorf("adding twice: %d", r.status)
	}
	if r := s.do(t, s.owner, "DELETE", fmt.Sprintf("/admin/v1/known-sources/%d", added.ID), ""); r.status != http.StatusOK {
		t.Errorf("DELETE: %d %s", r.status, r.body)
	}
	if r := s.do(t, s.owner, "DELETE", fmt.Sprintf("/admin/v1/known-sources/%d", added.ID), ""); r.status != http.StatusNotFound {
		t.Errorf("DELETE twice: %d", r.status)
	}
}

func TestTheAPITakesAnIncidentOnTheContainmentRoutes(t *testing.T) {
	s, k := withIncident(t)
	if r := s.do(t, s.owner, "POST", "/admin/v1/keys/"+k.Key.ID+"/disable", `{"incident_id":99}`); r.status != http.StatusNotFound {
		t.Errorf("disable with an unknown incident: %d %s", r.status, r.body)
	}
	if r := s.do(t, s.owner, "POST", "/admin/v1/keys/"+k.Key.ID+"/disable", `{"incident_id":1}`); r.status != http.StatusOK {
		t.Errorf("disable with the incident: %d %s", r.status, r.body)
	}
	// A route with no body still works, as before.
	if r := s.do(t, s.owner, "POST", "/admin/v1/keys/"+k.Key.ID+"/enable", ""); r.status != http.StatusOK {
		t.Errorf("enable: %d %s", r.status, r.body)
	}
	if r := s.do(t, s.owner, "POST", "/admin/v1/keys/"+k.Key.ID+"/rotate", `{"incident_id":1}`); r.status != http.StatusOK {
		t.Errorf("rotate with the incident: %d %s", r.status, r.body)
	}
	var d admin.IncidentDetail
	s.do(t, s.owner, "GET", "/admin/v1/incidents/1", "").decode(t, 200, &d)
	if d.State != "resolved" || d.Resolution != "confirmed" {
		t.Errorf("the rotation did not resolve the incident: %+v", d.Incident)
	}
}
