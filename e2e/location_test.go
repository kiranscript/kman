package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/policy/geoip/geoiptest"
)

// #89 through the real binary: a key used from one country is, during its
// learning period, only recorded when it turns up in a second; once its policy
// ends the learning period, a third country opens an incident, which alerts a
// channel with the key, the new place and what is usual, and a refused request
// from a fourth is counted on it too.
func TestANewCountryAfterTheLearningPeriodOpensAnIncidentThroughTheRealBinary(t *testing.T) {
	h := installed(t)
	dir := t.TempDir()
	// The test client is 127.0.0.1; it names the address a request came from in
	// X-Forwarded-For, which kman believes from a trusted proxy.
	country := geoiptest.Write(t, dir, "c.mmdb", "DBIP-Country-Lite",
		geoiptest.Range{Network: "127.0.0.0/8", Country: "US"}, geoiptest.Range{Network: "198.51.100.0/24", Country: "DE"},
		geoiptest.Range{Network: "203.0.113.0/24", Country: "FR"}, geoiptest.Range{Network: "192.0.2.0/24", Country: "NL"})
	var asn []geoiptest.Range
	for _, n := range []string{"127.0.0.0/8", "198.51.100.0/24", "203.0.113.0/24", "192.0.2.0/24"} {
		asn = append(asn, geoiptest.Range{Network: n, ASN: 64500, ASOrg: "Example Net"})
	}
	asnDB := geoiptest.Write(t, dir, "a.mmdb", "DBIP-ASN-Lite", asn...)
	h.appendConfig(t, "\ngeoip:\n  country_db: "+country+"\n  asn_db: "+asnDB+"\ntrusted_proxies:\n  - 127.0.0.1\n")
	s := h.serve(t)
	h.login(t, s)
	mock := newMock(t, "generic-json-thrice")
	h.addUpstream(t, "mock", mock.host, mock.port)
	secret := h.createKey(t, "my-app", "mock")
	pager := newSink(t, 200)
	h.addWebhook(t, "pager", pager)

	from := func(addr string) (*http.Response, string) {
		t.Helper()
		return sendThrough(t, s, "POST", "/v1/things", strings.NewReader(`{"name":"x"}`),
			http.Header{"Authorization": {"Bearer " + secret}, "X-Forwarded-For": {addr}})
	}
	// Events reach the store in batches, every 5 seconds.
	waitPlace := func(kind, value string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			out, _ := h.mustRun(t, "key", "places", "my-app", "--json", "--kind", kind)
			var got struct{ Places []struct{ Value string } }
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("key places --json printed %q: %v", out, err)
			}
			for _, pl := range got.Places {
				if pl.Value == value {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("the %s %s never became a place: %s", kind, value, out)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	if resp, body := from("127.0.0.1"); resp.StatusCode != 201 {
		t.Fatalf("= %d %s", resp.StatusCode, body)
	}
	waitPlace("country", "US")

	// Germany, during the learning period of 7 days: recorded, and nothing more.
	if resp, body := from("198.51.100.9"); resp.StatusCode != 201 {
		t.Fatalf("= %d %s", resp.StatusCode, body)
	}
	waitPlace("country", "DE")
	if got := h.openIncidents(t); len(got) != 0 {
		t.Fatalf("a new country during the learning period opened %+v", got)
	}

	// The key's own policy ends the learning period; France is then a detection.
	h.mustRun(t, "key", "update", "my-app", "--policy", `{"learning_period":"0s"}`)
	if resp, body := from("203.0.113.9"); resp.StatusCode != 201 {
		t.Fatalf("= %d %s", resp.StatusCode, body)
	}
	waitPlace("country", "FR")
	got := h.openIncidents(t)
	if len(got) != 1 || got[0].ID != 1 || got[0].Severity != "high" || got[0].State != "open" || len(got[0].Rules) != 2 || got[0].Rules[0] != "new_country" {
		t.Fatalf("incidents = %+v, want incident 1, high, with new_country and new_network", got)
	}
	alert := pager.waitFor(t, `"kind":"incident"`, "new_country", "new country FR", "US (1 request)", "203.0.113.9",
		"kman incident show 1", "kman key disable", "kman key rotate", "kman incident this-was-me 1")
	if strings.Contains(alert.body, secret) {
		t.Errorf("the alert holds the secret:\n%s", alert.body)
	}
	show, _ := h.mustRun(t, "incident", "show", "1")
	for _, want := range []string{"new_country fired (high)", "observed new country FR", "baseline US (1 request)"} {
		if !strings.Contains(show, want) {
			t.Errorf("incident show lacks %q:\n%s", want, show)
		}
	}
	if strings.Contains(show, "203.0.113.9") {
		t.Errorf("incident show gave out the client address:\n%s", show)
	}

	// An address lock that stops a leak still tells the operator: a refused
	// request from a fourth country counts on the incident.
	h.mustRun(t, "key", "update", "my-app", "--policy", `{"learning_period":"0s","allowed_countries":["US"]}`)
	resp, body := from("192.0.2.7")
	wantError(t, resp, body, http.StatusForbidden, "address_not_allowed")
	waitPlace("country", "NL")
	out, _ := h.mustRun(t, "incident", "show", "1", "--json")
	var detail struct {
		Detections []struct {
			Rule     string
			Evidence struct{ Country, Observed, Detail string }
		}
	}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatal(err)
	}
	var refused bool
	for _, d := range detail.Detections {
		if d.Rule == "new_country" && d.Evidence.Country == "NL" && strings.Contains(d.Evidence.Detail, "refused") && strings.Contains(d.Evidence.Detail, "address_not_allowed") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("incident 1 has no new_country detection for the refused request from NL:\n%s", out)
	}
}
