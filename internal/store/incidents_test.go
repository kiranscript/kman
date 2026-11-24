package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// withKeys opens a store holding two keys, abcd1234 and beef5678, and a canary,
// cafe0001.
func withKeys(t *testing.T) *store.Store {
	t.Helper()
	s := withCanary(t)
	do(t, s, func(tx *store.Tx) error {
		for _, k := range []store.VirtualKey{virtualKey("abcd1234", "app"), virtualKey("beef5678", "batch")} {
			if _, err := tx.CreateKey(k, hash); err != nil {
				return err
			}
		}
		return nil
	})
	return s
}

func newIncident(key string, sev store.Severity, rule string, at time.Time) store.Incident {
	return store.Incident{KeyID: key, Severity: sev, RulesJSON: `["` + rule + `"]`, Detections: 1, OpenedAt: at, LastFiredAt: at}
}

func TestAnIncidentOpensOpenAndReadsBack(t *testing.T) {
	s := withKeys(t)
	do(t, s, func(tx *store.Tx) error {
		created, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityHigh, "new_country", now))
		if err != nil {
			return err
		}
		if created.ID != 1 || created.State != store.IncidentOpen || created.Severity != store.SeverityHigh ||
			created.AlertedSeverity != "" || created.Generation == 0 || !created.OpenedAt.Equal(now) || !created.LastFiredAt.Equal(now) ||
			created.Detections != 1 || !created.ResolvedAt.IsZero() || created.Resolution != "" {
			t.Errorf("CreateIncident = %+v", created)
		}
		got, err := tx.Incident(created.ID)
		if err != nil || got != created {
			t.Errorf("Incident = %+v (%v), want %+v", got, err, created)
		}
		if rules := got.Rules(); len(rules) != 1 || rules[0] != "new_country" {
			t.Errorf("Rules = %v", rules)
		}
		unresolved, err := tx.UnresolvedIncident("abcd1234")
		if err != nil || unresolved.ID != created.ID {
			t.Errorf("UnresolvedIncident = %+v (%v)", unresolved, err)
		}
		if _, err := tx.UnresolvedIncident("beef5678"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UnresolvedIncident of a key with none = %v, want ErrNotFound", err)
		}
		if _, err := tx.Incident(99); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Incident(99) = %v, want ErrNotFound", err)
		}
		return nil
	})
}

func TestAKeyHasOneUnresolvedIncident(t *testing.T) {
	s := withKeys(t)
	do(t, s, func(tx *store.Tx) error {
		first, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityLow, "r", now))
		if err != nil {
			return err
		}
		if _, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityHigh, "r2", now)); !errors.Is(err, store.ErrConflict) {
			t.Errorf("a second unresolved incident for the key = %v, want ErrConflict", err)
		}
		// Another key is its own case.
		if _, err := tx.CreateIncident(newIncident("beef5678", store.SeverityLow, "r", now)); err != nil {
			t.Errorf("an incident for another key: %v", err)
		}
		// An acknowledged incident is still unresolved.
		first.State, first.AcknowledgedAt, first.AcknowledgedBy = store.IncidentAcknowledged, now, "owner"
		if _, err := tx.UpdateIncident(first); err != nil {
			return err
		}
		if _, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityHigh, "r2", now)); !errors.Is(err, store.ErrConflict) {
			t.Errorf("an incident beside an acknowledged one = %v, want ErrConflict", err)
		}
		// Once resolved, the key may have a new one, with a new id.
		first.State, first.Resolution, first.ResolvedAt, first.ResolvedBy = store.IncidentResolved, store.ResolutionConfirmed, now, "owner"
		if _, err := tx.UpdateIncident(first); err != nil {
			return err
		}
		next, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityHigh, "r2", now))
		if err != nil || next.ID == first.ID {
			t.Errorf("an incident after a resolved one = %+v (%v)", next, err)
		}
		return nil
	})
}

func TestIncidentsListNewestFirstAndFilter(t *testing.T) {
	s := withKeys(t)
	var ids [3]int64
	do(t, s, func(tx *store.Tx) error {
		a, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityLow, "r", now))
		if err != nil {
			return err
		}
		a.State, a.Resolution, a.ResolvedAt, a.ResolvedBy = store.IncidentResolved, store.ResolutionExpired, now, "system:detect"
		if _, err := tx.UpdateIncident(a); err != nil {
			return err
		}
		b, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityHigh, "r", now.Add(time.Minute)))
		if err != nil {
			return err
		}
		c, err := tx.CreateIncident(newIncident("beef5678", store.SeverityMedium, "r", now.Add(2*time.Minute)))
		if err != nil {
			return err
		}
		ids = [3]int64{a.ID, b.ID, c.ID}
		return nil
	})
	do(t, s, func(tx *store.Tx) error {
		all, err := tx.Incidents(store.IncidentFilter{})
		if err != nil || len(all) != 3 || all[0].ID != ids[2] || all[2].ID != ids[0] {
			t.Errorf("Incidents = %+v (%v), want all three, newest first", all, err)
		}
		open, err := tx.Incidents(store.IncidentFilter{UnresolvedOnly: true})
		if err != nil || len(open) != 2 || open[0].ID != ids[2] || open[1].ID != ids[1] {
			t.Errorf("unresolved Incidents = %+v (%v)", open, err)
		}
		forKey, err := tx.Incidents(store.IncidentFilter{KeyID: "abcd1234"})
		if err != nil || len(forKey) != 2 || forKey[0].ID != ids[1] {
			t.Errorf("Incidents for a key = %+v (%v)", forKey, err)
		}
		page, err := tx.Incidents(store.IncidentFilter{Before: ids[2], Limit: 1})
		if err != nil || len(page) != 1 || page[0].ID != ids[1] {
			t.Errorf("Incidents before %d, limit 1 = %+v (%v)", ids[2], page, err)
		}
		return nil
	})
}

func TestOnlyAnUnacknowledgedQuietIncidentBelowCriticalCanExpire(t *testing.T) {
	s := withKeys(t)
	old := now.Add(-8 * 24 * time.Hour)
	do(t, s, func(tx *store.Tx) error {
		mk := func(key string, sev store.Severity, mutate func(*store.Incident)) int64 {
			i, err := tx.CreateIncident(newIncident(key, sev, "r", old))
			if err != nil {
				t.Fatal(err)
			}
			if mutate != nil {
				mutate(&i)
				if i, err = tx.UpdateIncident(i); err != nil {
					t.Fatal(err)
				}
			}
			return i.ID
		}
		plain := mk("abcd1234", store.SeverityHigh, nil)
		mk("beef5678", store.SeverityHigh, func(i *store.Incident) {
			i.State, i.AcknowledgedAt, i.AcknowledgedBy = store.IncidentAcknowledged, old, "owner"
		})
		mk("cafe0001", store.SeverityCritical, nil)
		got, err := tx.ExpirableIncidents(now.Add(-7*24*time.Hour), 10)
		if err != nil || len(got) != 1 || got[0].ID != plain {
			t.Errorf("ExpirableIncidents = %+v (%v), want only the plain high one", got, err)
		}
		// Not yet quiet for 7 days.
		got, err = tx.ExpirableIncidents(old.Add(-time.Hour), 10)
		if err != nil || len(got) != 0 {
			t.Errorf("ExpirableIncidents with a cutoff before it fired = %+v (%v), want none", got, err)
		}
		return nil
	})
}

func TestDetectionsAreRecordedInOrderAndCascadeFromTheIncident(t *testing.T) {
	s := withKeys(t)
	do(t, s, func(tx *store.Tx) error {
		inc, err := tx.CreateIncident(newIncident("abcd1234", store.SeverityMedium, "new_asn", now))
		if err != nil {
			return err
		}
		for i, rule := range []string{"new_asn", "new_country"} {
			d, err := tx.AddDetection(store.Detection{IncidentID: inc.ID, KeyID: "abcd1234", At: now.Add(time.Duration(i) * time.Minute),
				Rule: rule, Severity: store.SeverityMedium, EvidenceJSON: `{"country":"DE"}`})
			if err != nil || d.ID == 0 {
				t.Fatalf("AddDetection = %+v (%v)", d, err)
			}
		}
		ds, err := tx.Detections(inc.ID, 10)
		if err != nil || len(ds) != 2 || ds[0].Rule != "new_asn" || ds[1].Rule != "new_country" || ds[0].EvidenceJSON != `{"country":"DE"}` ||
			!ds[1].At.Equal(now.Add(time.Minute)) || ds[0].Severity != store.SeverityMedium || ds[0].KeyID != "abcd1234" {
			t.Errorf("Detections = %+v (%v)", ds, err)
		}
		if ds, _ = tx.Detections(inc.ID, 1); len(ds) != 1 {
			t.Errorf("Detections with a limit of 1 = %d rows", len(ds))
		}
		if _, err := tx.AddDetection(store.Detection{IncidentID: 99, KeyID: "abcd1234", At: now, Rule: "r", Severity: store.SeverityLow}); err == nil {
			t.Error("a detection for an incident that does not exist was accepted")
		}
		return nil
	})
}

func TestTheAuditEntriesOfAnIncidentAreItsTimeline(t *testing.T) {
	s := withKeys(t)
	do(t, s, func(tx *store.Tx) error {
		for _, e := range []store.AuditEntry{
			{TS: now, Actor: "system:detect", Action: "incident.open", TargetKind: "incident", TargetID: "1", IncidentID: 1},
			{TS: now, Actor: "owner", Action: "key.disable", TargetKind: "key", TargetID: "abcd1234", IncidentID: 1},
			{TS: now, Actor: "owner", Action: "key.disable", TargetKind: "key", TargetID: "beef5678"},
			{TS: now, Actor: "owner", Action: "incident.resolve", TargetKind: "incident", TargetID: "2", IncidentID: 2},
		} {
			if _, err := tx.AppendAudit(e); err != nil {
				return err
			}
		}
		got, err := tx.AuditEntriesForIncident(1)
		if err != nil || len(got) != 2 || got[0].Action != "incident.open" || got[1].Action != "key.disable" {
			t.Errorf("AuditEntriesForIncident(1) = %+v (%v)", got, err)
		}
		return nil
	})
}

func TestSeveritiesRankLowToCritical(t *testing.T) {
	order := []store.Severity{store.SeverityLow, store.SeverityMedium, store.SeverityHigh, store.SeverityCritical}
	for i := 1; i < len(order); i++ {
		if order[i-1].Rank() >= order[i].Rank() {
			t.Errorf("%s does not rank below %s", order[i-1], order[i])
		}
	}
	if store.Severity("urgent").Rank() != 0 || store.Severity("").Rank() != 0 {
		t.Error("an unknown severity has a rank")
	}
	if s, err := store.ParseSeverity("High"); err != nil || s != store.SeverityHigh {
		t.Errorf("ParseSeverity(High) = %q, %v", s, err)
	}
	if _, err := store.ParseSeverity("urgent"); err == nil {
		t.Error("ParseSeverity accepts urgent")
	}
}
