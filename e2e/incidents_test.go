package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type incidentShown struct {
	ID             int64
	State          string
	Severity       string
	Resolution     string
	DetectionCount int64 `json:"detection_count"`
	Rules          []string
}

func (h *home) openIncidents(t *testing.T) []incidentShown {
	t.Helper()
	out, _ := h.mustRun(t, "incidents", "--open", "--json")
	var list struct{ Incidents []incidentShown }
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("incidents --json printed %q: %v", out, err)
	}
	return list.Incidents
}

// #88 end to end, by CLI alone: a canary's use opens a critical incident, its
// alert names the incident, a repeat only counts, the incident is acknowledged
// and resolved, and a later use opens a new one that revoking the canary with
// --incident resolves as confirmed, with the timeline and audit entries right.
func TestACanaryOpensAnIncidentThatIsAcknowledgedAndResolvedByCLI(t *testing.T) {
	t.Parallel()
	p := behindKman(t, "")
	pager := newSink(t, 200)
	p.h.addWebhook(t, "pager", pager)
	out, _ := p.h.mustRun(t, "canary", "create", "decoy", "--planted", "the deploy repo's .env", "--json")
	var created struct {
		Canary struct{ ID string }
		Secret string
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatal(err)
	}
	use := func() {
		t.Helper()
		resp, body := sendThrough(t, p.s, "POST", "/v1/chat/completions", strings.NewReader("{}"), http.Header{"Authorization": {"Bearer " + created.Secret}})
		wantError(t, resp, body, 401, "invalid_key")
	}
	waitIncidents := func(want func([]incidentShown) bool, what string) []incidentShown {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			got := p.h.openIncidents(t)
			if want(got) {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: incidents = %+v", what, got)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	if got := p.h.openIncidents(t); len(got) != 0 {
		t.Fatalf("incidents before any use: %+v", got)
	}
	use()
	got := waitIncidents(func(l []incidentShown) bool { return len(l) == 1 }, "the use opened no incident")
	if got[0].ID != 1 || got[0].Severity != "critical" || got[0].State != "open" || len(got[0].Rules) != 1 || got[0].Rules[0] != "canary" {
		t.Fatalf("incident = %+v, want a critical open incident of the canary rule", got[0])
	}
	// The canary's alert names the incident and how to respond.
	alert := pager.waitFor(t, `"kind":"canary"`, `"incident_id":1`, "kman incident show 1", "kman canary reset "+created.Canary.ID)
	if strings.Contains(alert.body, created.Secret) {
		t.Errorf("the alert holds the secret:\n%s", alert.body)
	}

	// More uses are counted on the one incident; none of them is a second incident.
	use()
	use()
	waitIncidents(func(l []incidentShown) bool { return len(l) == 1 && l[0].DetectionCount >= 3 }, "repeats were not counted")
	if n := pager.count(`"kind":"incident"`); n != 0 {
		t.Errorf("the incident alerted %d times beside the canary's own alert", n)
	}

	list, _ := p.h.mustRun(t, "incidents")
	if !strings.Contains(list, "decoy") || !strings.Contains(list, "critical") || !strings.Contains(list, "canary") {
		t.Errorf("incidents:\n%s", list)
	}
	show, _ := p.h.mustRun(t, "incident", "show", "1")
	for _, want := range []string{"decoy", "critical", "canary fired", "opened by system:detect"} {
		if !strings.Contains(show, want) {
			t.Errorf("incident show lacks %q:\n%s", want, show)
		}
	}
	if strings.Contains(show, "127.0.0.1") {
		t.Errorf("incident show gave out the client address:\n%s", show)
	}

	p.h.mustRun(t, "incident", "ack", "1")
	if got := p.h.openIncidents(t); len(got) != 1 || got[0].State != "acknowledged" {
		t.Errorf("after ack: %+v", got)
	}
	// A canary's incident never expires on its own, and says so by staying open.
	p.h.mustRun(t, "incident", "resolve", "1", "--as", "false-positive")
	if got := p.h.openIncidents(t); len(got) != 0 {
		t.Errorf("after resolve: %+v", got)
	}

	// The next use opens a new incident, and revoking the canary for it resolves it.
	p.h.mustRun(t, "canary", "reset", "decoy")
	use()
	got = waitIncidents(func(l []incidentShown) bool { return len(l) == 1 }, "the next use opened no incident")
	if got[0].ID != 2 {
		t.Fatalf("incident = %+v, want a new one, 2", got[0])
	}
	if _, stderr, code := p.h.run(t, "key", "revoke", "decoy", "--incident", "1"); code == 0 || !strings.Contains(stderr, "already resolved") {
		t.Errorf("revoking for a resolved incident: exit %d, %s", code, stderr)
	}
	p.h.mustRun(t, "key", "revoke", "decoy", "--incident", "2")
	if got := p.h.openIncidents(t); len(got) != 0 {
		t.Errorf("after revoking for it: %+v", got)
	}

	// The audit log has every state change with the incident id.
	audit, _ := p.h.mustRun(t, "audit", "--json")
	for _, want := range []string{`"action":"incident.open"`, `"action":"incident.acknowledge"`, `"action":"incident.resolve"`, `"action":"key.revoke"`, `"incident_id":2`, `"actor":"system:detect"`} {
		if !strings.Contains(strings.ReplaceAll(audit, ` `, ``), want) {
			t.Errorf("the audit log lacks %s", want)
		}
	}
	timeline, _ := p.h.mustRun(t, "incident", "show", "2")
	for _, want := range []string{"resolved", "confirmed", "key.revoke key"} {
		if !strings.Contains(timeline, want) {
			t.Errorf("incident 2's timeline lacks %q:\n%s", want, timeline)
		}
	}
}
