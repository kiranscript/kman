package alerts_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/detect"
	"github.com/kirank55/kman/internal/events"
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
			o.CanaryIncident = func(tx *store.Tx, keyID string, at time.Time, ev store.Evidence) (int64, bool, error) {
				out, err := eng.Raise(tx, detect.Detection{KeyID: keyID, Rule: "canary", Severity: store.SeverityCritical, At: at, Evidence: ev, Silent: true})
				return out.Incident.ID, out.Change != detect.Repeated, err
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

// A canary used again soon after its incident was resolved opens a new incident,
// and that is news: it alerts even inside the canary's throttle window (#112).
func TestACanaryReusedRightAfterItsIncidentIsResolvedAlertsForTheNewOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var eng *detect.Engine
		in := newInstall(t, func(o *alerts.Options) {
			o.CanaryIncident = func(tx *store.Tx, keyID string, at time.Time, ev store.Evidence) (int64, bool, error) {
				out, err := eng.Raise(tx, detect.Detection{KeyID: keyID, Rule: "canary", Severity: store.SeverityCritical, At: at, Evidence: ev, Silent: true})
				return out.Incident.ID, out.Change != detect.Repeated, err
			}
		})
		eng = in.engine()
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 1 {
			t.Fatalf("%d alerts for the first use, want 1", n)
		}
		err := in.store.Do(context.Background(), func(tx *store.Tx) error {
			inc, err := tx.Incident(1)
			if err != nil {
				return err
			}
			inc.State, inc.Resolution, inc.ResolvedAt, inc.ResolvedBy = store.IncidentResolved, "confirmed", time.Now(), "owner"
			_, err = tx.UpdateIncident(inc)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute) // well inside the canary's 10-minute window
		bus.Emit(canaryUse("cafe0001", "198.51.100.9"))
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 2 || !strings.Contains(got[1].Body, "kman incident show 2") {
			t.Fatalf("after the resolve: %d alerts, want a second naming incident 2", len(got))
		}
		// Its repeats are held back as before.
		bus.Emit(canaryUse("cafe0001", "198.51.100.9"))
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 2 {
			t.Errorf("a repeat on the new incident alerted: %d alerts", n)
		}
	})
}

// A client hammering a canary costs the store a transaction per batch of the
// priority lane, not one per use, and another canary's first use behind it
// still alerts (#114).
func TestAHammeredCanaryCostsATransactionPerBatchAndHoldsUpNoOther(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var eng *detect.Engine
		in := newInstall(t, func(o *alerts.Options) {
			o.CanaryIncident = func(tx *store.Tx, keyID string, at time.Time, ev store.Evidence) (int64, bool, error) {
				out, err := eng.Raise(tx, detect.Detection{KeyID: keyID, Rule: "canary", Severity: store.SeverityCritical, At: at, Evidence: ev, Silent: true})
				return out.Incident.ID, out.Change != detect.Repeated, err
			}
		})
		eng = in.engine()
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
		in.withCanary("cafe0002", "decoy-2", "the wiki")
		in.withKey("abcd1234", "ci-bot")
		bus, stop := in.priorityLane()
		defer stop()
		for i := range 200 {
			bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
			if i%4 == 0 {
				bus.Emit(disabledUse("abcd1234", "198.51.100.1"))
			}
		}
		bus.Emit(canaryUse("cafe0002", "192.0.2.9"))
		synctest.Wait()
		got := in.net.received("hooks.test")
		var bodies []string
		for _, r := range got {
			bodies = append(bodies, r.Body)
		}
		all := strings.Join(bodies, "\n")
		if len(got) != 2 || !strings.Contains(all, "kman canary reset cafe0001") || !strings.Contains(all, "kman canary reset cafe0002") {
			t.Fatalf("%d alerts, want one for each canary's first use:\n%s", len(got), all)
		}
		if n := in.svc.PriorityTransactions(); n > 20 {
			t.Errorf("%d transactions for 251 priority events, want a few batches", n)
		}
		var c store.Canary
		var inc store.Incident
		in.store.Do(context.Background(), func(tx *store.Tx) (err error) {
			if c, err = tx.Canary("cafe0001"); err != nil {
				return err
			}
			inc, err = tx.Incident(1)
			return err
		})
		if c.Fires != 200 || inc.Detections != 200 {
			t.Errorf("canary fires %d, incident counted %d; want every one of 200 uses", c.Fires, inc.Detections)
		}
	})
}

// backlog starts the priority lane only once events are waiting in it, so
// they are read as batches and not as they come.
func (in *install) backlog(es ...events.Event) (stop func()) {
	bus := events.New(events.Options{})
	sub := bus.Subscribe("alerts", events.SubOptions{PriorityOnly: true, Buffer: events.DefaultPriorityCapacity})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { bus.Run(ctx) })
	for _, e := range es {
		bus.Emit(e)
	}
	synctest.Wait()
	wg.Go(func() { in.svc.RunPriority(sub) })
	stopRun := in.run()
	return func() { cancel(); wg.Wait(); stopRun() }
}

// A batch that fails is handled event by event, so one canary whose incident
// cannot be recorded costs no other its alert, and what the failed batch did
// is not done twice.
func TestAFailingPriorityEventCostsNoOtherInItsBatchItsAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t, func(o *alerts.Options) {
			o.CanaryIncident = func(_ *store.Tx, keyID string, _ time.Time, _ store.Evidence) (int64, bool, error) {
				if keyID == "cafe0001" {
					return 0, false, errors.New("the incident cannot be recorded")
				}
				return 7, true, nil
			}
		})
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
		in.withCanary("cafe0002", "decoy-2", "the wiki")
		// The one that works comes first, so the failed batch rolls it back.
		stop := in.backlog(canaryUse("cafe0002", "192.0.2.9"), canaryUse("cafe0001", "203.0.113.7"))
		defer stop()
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 || !strings.Contains(got[0].Body, "kman canary reset cafe0002") {
			t.Fatalf("%d alerts, want the second canary's alone", len(got))
		}
		var c store.Canary
		in.store.Do(context.Background(), func(tx *store.Tx) (err error) { c, err = tx.Canary("cafe0002"); return })
		if c.Fires != 1 {
			t.Errorf("the canary that worked has %d fires, want 1", c.Fires)
		}
		if n := in.svc.PriorityTransactions(); n != 3 {
			t.Errorf("%d transactions, want the batch and then one for each event", n)
		}
	})
}

// What is waiting is read in batches of at most maxPriorityBatch (64), each in
// one transaction, and every use is recorded.
func TestABacklogOfCanaryUsesIsReadInBoundedBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
		var es []events.Event
		for range 300 {
			es = append(es, canaryUse("cafe0001", "203.0.113.7"))
		}
		stop := in.backlog(es...)
		defer stop()
		synctest.Wait()
		if n := in.svc.PriorityTransactions(); n != 5 {
			t.Errorf("%d transactions for 300 waiting uses, want 5 batches of at most 64", n)
		}
		if n := len(in.net.received("hooks.test")); n != 1 {
			t.Errorf("%d alerts, want the first use's alone", n)
		}
		var c store.Canary
		in.store.Do(context.Background(), func(tx *store.Tx) (err error) { c, err = tx.Canary("cafe0001"); return })
		if c.Fires != 300 {
			t.Errorf("fires = %d, want 300", c.Fires)
		}
	})
}

// An address on a canary's ignore list is no use at all: it opens no incident.
func TestAnIgnoredCanaryUseOpensNoIncident(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		called := 0
		in := newInstall(t, func(o *alerts.Options) {
			o.CanaryIncident = func(*store.Tx, string, time.Time, store.Evidence) (int64, bool, error) { called++; return 1, true, nil }
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
			o.CanaryIncident = func(*store.Tx, string, time.Time, store.Evidence) (int64, bool, error) { called++; return 1, true, nil }
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

// #89: a location detection's alert names the key, the new place with its label,
// the places the key normally uses, and the commands that contain it.
func TestALocationAlertNamesTheKeyThePlaceItsLabelTheUsualPlacesAndHowToContainIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", true, "")
		in.withKey("abcd1234", "ci-bot")
		stop := in.run()
		defer stop()
		in.raise(in.engine(), "abcd1234", "new_asn", store.SeverityMedium, store.Evidence{
			Address: "203.0.113.9", Network: "203.0.113.0/24", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Client: "curl/8",
			Observed: "new ASN AS3320 (Deutsche Telekom AG)", Baseline: "AS64500 Example Net (412 requests)", Usual: "198.51.100.0/24 (412 requests), 203.0.113.0/24 (30 requests), 192.0.2.0/24 (9 requests), and 4 more",
			Detail: "the request was refused (address_not_allowed)"})
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1", len(got))
		}
		for _, want := range []string{"ci-bot", "abcd1234", "new_asn",
			"New place", "new ASN AS3320 (Deutsche Telekom AG)",
			"Usually used from", "AS64500 Example Net (412 requests)", "198.51.100.0/24 (412 requests), 203.0.113.0/24 (30 requests), 192.0.2.0/24 (9 requests), and 4 more",
			"203.0.113.9", "refused",
			"kman key disable abcd1234 --incident 1", "kman key rotate abcd1234 --incident 1", "kman incident this-was-me 1"} {
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("the alert lacks %q:\n%s", want, got[0].Body)
			}
		}
	})
}
