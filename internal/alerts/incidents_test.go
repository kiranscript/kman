package alerts_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/detect"
	"github.com/kirank55/kman/internal/store"
)

// engine is the incident engine over the install, alerting through its service,
// as the composition root wires them.
func (in *install) engine() *detect.Engine {
	return detect.New(detect.Options{Store: in.store, Now: time.Now, Alerts: in.svc})
}

func (in *install) raise(e *detect.Engine, key, rule string, sev store.Severity, ev store.Evidence) detect.Outcome {
	in.t.Helper()
	out, err := e.RaiseNow(context.Background(), detect.Detection{KeyID: key, Rule: rule, Severity: sev, Evidence: ev})
	if err != nil {
		in.t.Fatal(err)
	}
	return out
}

func TestAnIncidentAlertsWhenItOpensAndWhenItEscalatesAndNotOnRepeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.withKey("abcd1234", "ci-bot")
		stop := in.run()
		defer stop()
		e := in.engine()
		ev := store.Evidence{Address: "203.0.113.7", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Client: "curl/8", Observed: "DE", Baseline: "US, GB"}
		in.raise(e, "abcd1234", "new_country", store.SeverityMedium, ev)
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts after the opening, want 1", len(got))
		}
		for _, want := range []string{"MEDIUM", "Incident opened", "ci-bot", "abcd1234", "new_country", "203.0.113.7", "DE", "AS3320", "Deutsche Telekom AG",
			"curl/8", "DE", "US, GB", "kman incident show 1", "kman incident ack 1", "kman key disable abcd1234 --incident 1", "kman incident this-was-me 1"} {
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("the alert lacks %q:\n%s", want, got[0].Body)
			}
		}
		in.raise(e, "abcd1234", "new_country", store.SeverityMedium, ev) // a repeat
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 1 {
			t.Fatalf("a repeat alerted: %d alerts", n)
		}
		in.raise(e, "abcd1234", "new_asn", store.SeverityHigh, ev) // a new rule, and higher
		synctest.Wait()
		got = in.net.received("hooks.test")
		if len(got) != 2 || !strings.Contains(got[1].Body, "Incident escalated") || !strings.Contains(got[1].Body, "HIGH") ||
			!strings.Contains(got[1].Body, "new_country, new_asn") {
			t.Fatalf("alerts = %+v, want an escalation naming both rules", got)
		}
	})
}

func TestAnIncidentAlertCarriesTheIncidentIDInAWebhook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "webhook", "hooks.test", "low", true, "s3cret")
		in.withKey("abcd1234", "ci-bot")
		stop := in.run()
		defer stop()
		in.raise(in.engine(), "abcd1234", "new_country", store.SeverityLow, store.Evidence{Country: "DE"})
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1", len(got))
		}
		var p struct {
			Kind       string   `json:"kind"`
			Severity   string   `json:"severity"`
			IncidentID int64    `json:"incident_id"`
			Rules      []string `json:"rules"`
		}
		if err := json.Unmarshal([]byte(got[0].Body), &p); err != nil || p.Kind != "incident" || p.Severity != "low" || p.IncidentID != 1 || len(p.Rules) != 1 {
			t.Errorf("payload = %+v (%v)", p, err)
		}
	})
}

// A channel's minimum severity filters incident alerts like any other: low
// detections show only in the CLI and dashboard.
func TestAnIncidentAlertRespectsAChannelsMinimumSeverity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.withKey("abcd1234", "ci-bot")
		stop := in.run()
		defer stop()
		in.raise(in.engine(), "abcd1234", "new_asn", store.SeverityLow, store.Evidence{ASN: 1})
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 0 {
			t.Errorf("a low incident reached a medium channel: %d alerts", n)
		}
	})
}

func TestAHighIncidentAlertTakesThePriorityLane(t *testing.T) {
	in := newInstall(t)
	in.channel("ch_1", "slack", "hooks.test", "low", true, "")
	in.withKey("abcd1234", "ci-bot")
	for _, sev := range []store.Severity{store.SeverityMedium, store.SeverityHigh} {
		in.raise(in.engine(), "abcd1234", "rule_"+string(sev), sev, store.Evidence{})
	}
	var pri []bool
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		due, err := tx.DueAlerts("ch_1", time.Now().Add(time.Hour), 10)
		for _, a := range due {
			pri = append(pri, a.Priority)
		}
		return err
	})
	if err != nil || len(pri) != 2 || !pri[0] || pri[1] {
		t.Errorf("priority flags = %v (%v), want the high one first and priority, the medium one not", pri, err)
	}
}

func TestAnIncidentAlertCleansWhatCameFromADatabaseOrAHeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", true, "")
		in.withKey("abcd1234", "ci-bot")
		stop := in.run()
		defer stop()
		in.raise(in.engine(), "abcd1234", "new_client", store.SeverityMedium, store.Evidence{Client: "evil\nINJECT: line", ASOrg: strings.Repeat("x", 500)})
		synctest.Wait()
		body := in.net.received("hooks.test")[0].Body
		if strings.Contains(body, "\nINJECT") || strings.Contains(body, strings.Repeat("x", 100)) {
			t.Errorf("the alert holds uncleaned evidence:\n%s", body)
		}
	})
}

// Canary wiring (#88): a canary's use opens or escalates a critical incident
// for the canary key, and its priority-lane alert names the incident. The
// alert's own policy (the first use always, then one per window) is unchanged.
func TestACanaryUseOpensACriticalIncidentAndItsAlertNamesIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var eng *detect.Engine
		in := newInstall(t, func(o *alerts.Options) {
			o.CanaryIncident = func(tx *store.Tx, keyID string, at time.Time, ev store.Evidence) (int64, error) {
				out, err := eng.Raise(tx, detect.Detection{KeyID: keyID, Rule: "canary", Severity: store.SeverityCritical, At: at, Evidence: ev, Silent: true})
				return out.Incident.ID, err
			}
		})
		eng = in.engine()
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
		bus, stop := in.priorityLane()
		defer stop()
		e := canaryUse("cafe0001", "203.0.113.7")
		e.Country, e.ASN, e.Client = "DE", 3320, "curl/8"
		bus.Emit(e)
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: the canary's own, and no second one from the incident", len(got))
		}
		for _, want := range []string{"CRITICAL", "A canary key was used", "kman incident show 1", "kman canary reset cafe0001"} {
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("the alert lacks %q:\n%s", want, got[0].Body)
			}
		}
		var inc store.Incident
		var ds []store.Detection
		in.store.Do(context.Background(), func(tx *store.Tx) (err error) {
			if inc, err = tx.Incident(1); err != nil {
				return err
			}
			ds, err = tx.Detections(1, 10)
			return err
		})
		var ev store.Evidence
		if inc.KeyID != "cafe0001" || inc.Severity != store.SeverityCritical || inc.Detections != 1 || len(ds) != 1 || ds[0].Rule != "canary" ||
			json.Unmarshal([]byte(ds[0].EvidenceJSON), &ev) != nil || ev.Address != "203.0.113.7" || ev.Country != "DE" || ev.ASN != 3320 || ev.Client != "curl/8" ||
			!strings.Contains(ev.Detail, "the deploy repo's .env") {
			t.Errorf("incident = %+v, detections = %+v", inc, ds)
		}
		// Held-back repeats are counted on the one incident, and alert as before.
		bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		bus.Emit(canaryUse("cafe0001", "203.0.113.8"))
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 1 {
			t.Errorf("repeats inside the window alerted: %d alerts", n)
		}
		in.store.Do(context.Background(), func(tx *store.Tx) (err error) { inc, err = tx.Incident(1); return })
		if inc.Detections != 3 {
			t.Errorf("the incident counted %d uses, want 3", inc.Detections)
		}
		// A reset arms it again: the next use alerts at once, on the same incident.
		in.resetCanary("cafe0001")
		bus.Emit(canaryUse("cafe0001", "198.51.100.1"))
		synctest.Wait()
		got = in.net.received("hooks.test")
		if len(got) != 2 || !strings.Contains(got[1].Body, "kman incident show 1") {
			t.Errorf("after a reset: %d alerts, want a second naming incident 1", len(got))
		}
	})
}

// An address on a canary's ignore list is no use at all: it opens no incident.
func TestAnIgnoredCanaryUseOpensNoIncident(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		called := 0
		in := newInstall(t, func(o *alerts.Options) {
			o.CanaryIncident = func(*store.Tx, string, time.Time, store.Evidence) (int64, error) { called++; return 1, nil }
		})
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the wiki", "192.0.2.0/24")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(canaryUse("cafe0001", "192.0.2.50"))
		synctest.Wait()
		if called != 0 {
			t.Errorf("an ignored use raised %d detections", called)
		}
	})
}

// A disabled key's use is not a canary's: it opens no incident here.
func TestADisabledKeyUseOpensNoIncident(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		called := 0
		in := newInstall(t, func(o *alerts.Options) {
			o.CanaryIncident = func(*store.Tx, string, time.Time, store.Evidence) (int64, error) { called++; return 1, nil }
		})
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.withKey("abcd1234", "ci-bot")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(disabledUse("abcd1234", "203.0.113.7"))
		synctest.Wait()
		if called != 0 || len(in.net.received("hooks.test")) != 1 {
			t.Errorf("hook called %d times, %d alerts", called, len(in.net.received("hooks.test")))
		}
	})
}

// Without the hook a canary alerts as it always did.
func TestACanaryWithoutIncidentsAlertsAsBefore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the wiki")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 || strings.Contains(got[0].Body, "kman incident") {
			t.Errorf("got %+v", got)
		}
	})
}

func TestIncidentAuditEntriesAreAlertedByWhatTheyDo(t *testing.T) {
	cases := []struct {
		action string
		want   string
	}{
		{"incident.acknowledge", "LOW"}, {"incident.resolve", "MEDIUM"}, {"incident.this_was_me", "MEDIUM"},
		{"known_source.create", "MEDIUM"}, {"known_source.delete", "LOW"},
	}
	for _, c := range cases {
		t.Run(c.action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				in := newInstall(t)
				in.channel("ch_1", "slack", "hooks.test", "low", true, "")
				stop := in.run()
				defer stop()
				target := "incident"
				if strings.HasPrefix(c.action, "known_source") {
					target = "known_source"
				}
				in.audited(store.AuditEntry{Actor: "owner", Action: c.action, TargetKind: target, TargetID: "7", IncidentID: 7})
				synctest.Wait()
				got := in.net.received("hooks.test")
				if len(got) != 1 || !strings.Contains(got[0].Body, c.want) {
					t.Fatalf("got %+v, want one %s alert", got, c.want)
				}
				if target == "incident" && !strings.Contains(got[0].Body, "kman incident show 7") {
					t.Errorf("the alert does not say how to look at the incident:\n%s", got[0].Body)
				}
			})
		})
	}
}
