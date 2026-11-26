package detect_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kiranscript/kman/internal/detect"
	"github.com/kiranscript/kman/internal/store"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// alerter records the incident alerts the engine queues.
type alerter struct {
	mu     sync.Mutex
	alerts []queued
	wakes  int
	fail   error
}

type queued struct {
	change string
	inc    store.Incident
	det    store.Detection
}

func (a *alerter) AlertIncident(_ *store.Tx, change string, inc store.Incident, det store.Detection) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return a.fail
	}
	a.alerts = append(a.alerts, queued{change, inc, det})
	return nil
}

func (a *alerter) Wake() { a.mu.Lock(); a.wakes++; a.mu.Unlock() }

func (a *alerter) got() []queued {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]queued(nil), a.alerts...)
}

type rig struct {
	t      *testing.T
	store  *store.Store
	engine *detect.Engine
	alerts *alerter
	now    time.Time
}

// newRig is an engine over a store holding keys abcd1234 and beef5678 and the
// canary cafe0001. Its clock is the rig's now, which tests move.
func newRig(t *testing.T) *rig {
	t.Helper()
	st := seedStore(t,
		store.VirtualKey{ID: "abcd1234", Name: "app", CredentialID: "cr_1"},
		store.VirtualKey{ID: "beef5678", Name: "batch", CredentialID: "cr_1"},
		store.VirtualKey{ID: "cafe0001", Name: "decoy", Kind: store.KeyCanary})
	r := &rig{t: t, store: st, alerts: &alerter{}, now: t0}
	r.engine = detect.New(detect.Options{Store: st, Now: func() time.Time { return r.now }, Alerts: r.alerts})
	return r
}

// seedStore opens a store holding an upstream, a credential cr_1 and the keys.
func seedStore(t *testing.T, keys ...store.VirtualKey) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	err = st.Do(context.Background(), func(tx *store.Tx) error {
		if err := tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: t0}); err != nil {
			return err
		}
		u, err := tx.CreateUpstream(store.Upstream{Name: "openai", Preset: "openai", PresetVersion: 1, Profile: "openai", Host: "api.openai.com", Port: 443,
			TLS: true, PathAllowlistJSON: `["/v1/"]`, PriceMultiplierPPM: 1_000_000, CreatedAt: t0, UpdatedAt: t0})
		if err != nil {
			return err
		}
		if _, err := tx.CreateCredential(store.Credential{ID: "cr_1", UpstreamID: u.ID, Name: "main", Ciphertext: []byte("c"), Nonce: []byte("n"),
			DataKeyID: "dek_1", Last4: "abcd", CreatedAt: t0}); err != nil {
			return err
		}
		for _, k := range keys {
			if k.PolicyJSON == "" {
				k.PolicyJSON = `{}`
			}
			k.PolicyVersion, k.TagsJSON, k.CreatedAt = 1, `[]`, t0
			if _, err := tx.CreateKey(k, []byte("01234567890123456789012345678901")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (r *rig) raise(d detect.Detection) detect.Outcome {
	r.t.Helper()
	if d.At.IsZero() {
		d.At = r.now
	}
	out, err := r.engine.RaiseNow(context.Background(), d)
	if err != nil {
		r.t.Fatalf("Raise: %v", err)
	}
	return out
}

func (r *rig) incident(id int64) store.Incident {
	r.t.Helper()
	var i store.Incident
	if err := r.store.Do(context.Background(), func(tx *store.Tx) (err error) { i, err = tx.Incident(id); return }); err != nil {
		r.t.Fatal(err)
	}
	return i
}

func (r *rig) detections(id int64) []store.Detection {
	r.t.Helper()
	var ds []store.Detection
	if err := r.store.Do(context.Background(), func(tx *store.Tx) (err error) { ds, err = tx.Detections(id, 1000); return }); err != nil {
		r.t.Fatal(err)
	}
	return ds
}

func (r *rig) audit(id int64) []store.AuditEntry {
	r.t.Helper()
	var es []store.AuditEntry
	if err := r.store.Do(context.Background(), func(tx *store.Tx) (err error) { es, err = tx.AuditEntriesForIncident(id); return }); err != nil {
		r.t.Fatal(err)
	}
	return es
}

func (r *rig) update(id int64, fn func(*store.Incident)) {
	r.t.Helper()
	err := r.store.Do(context.Background(), func(tx *store.Tx) error {
		i, err := tx.Incident(id)
		if err != nil {
			return err
		}
		fn(&i)
		_, err = tx.UpdateIncident(i)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func det(key, rule string, sev store.Severity) detect.Detection {
	return detect.Detection{KeyID: key, Rule: rule, Severity: sev, Evidence: store.Evidence{Country: "DE", ASN: 3320}}
}

func TestADetectionOpensAnIncidentAndAlerts(t *testing.T) {
	r := newRig(t)
	out := r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	if out.Change != detect.Opened || out.Incident.ID != 1 || out.Incident.State != store.IncidentOpen ||
		out.Incident.Severity != store.SeverityMedium || out.Incident.AlertedSeverity != store.SeverityMedium ||
		out.Incident.Detections != 1 || !out.Incident.OpenedAt.Equal(t0) {
		t.Fatalf("Outcome = %+v", out)
	}
	got := r.alerts.got()
	if len(got) != 1 || got[0].change != "opened" || got[0].inc.ID != 1 || got[0].det.Rule != "new_country" {
		t.Fatalf("alerts = %+v, want one for the opening", got)
	}
	if r.alerts.wakes != 1 {
		t.Errorf("woke the dispatcher %d times, want 1", r.alerts.wakes)
	}
	ds := r.detections(1)
	var ev store.Evidence
	if len(ds) != 1 || json.Unmarshal([]byte(ds[0].EvidenceJSON), &ev) != nil || ev.Country != "DE" || ev.ASN != 3320 {
		t.Errorf("detections = %+v", ds)
	}
	es := r.audit(1)
	if len(es) != 1 || es[0].Action != "incident.open" || es[0].Actor != "system:detect" || es[0].TargetKind != "incident" || es[0].TargetID != "1" {
		t.Errorf("audit = %+v, want one incident.open by system:detect carrying the incident id", es)
	}
}

func TestARepeatOfTheSameRuleOnlyCountsAndNeverAlerts(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	r.now = t0.Add(10 * time.Minute)
	out := r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	if out.Change != detect.Repeated || out.Incident.ID != 1 || out.Incident.Detections != 2 || !out.Incident.LastFiredAt.Equal(r.now) {
		t.Fatalf("Outcome = %+v", out)
	}
	if n := len(r.alerts.got()); n != 1 {
		t.Errorf("%d alerts, want only the opening", n)
	}
	if es := r.audit(1); len(es) != 1 {
		t.Errorf("a repeat wrote audit entries: %+v", es)
	}
	if r.wakes() != 1 {
		t.Errorf("a repeat woke the dispatcher")
	}
}

func (r *rig) wakes() int { return r.alerts.wakes }

func TestAHigherSeverityEscalatesAndAlerts(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	r.now = t0.Add(time.Minute)
	out := r.raise(det("abcd1234", "new_country", store.SeverityHigh))
	if out.Change != detect.Escalated || out.Incident.Severity != store.SeverityHigh || out.Incident.AlertedSeverity != store.SeverityHigh {
		t.Fatalf("Outcome = %+v", out)
	}
	got := r.alerts.got()
	if len(got) != 2 || got[1].change != "escalated" {
		t.Fatalf("alerts = %+v, want an opening and an escalation", got)
	}
	if es := r.audit(1); len(es) != 2 || es[1].Action != "incident.escalate" {
		t.Errorf("audit = %+v", es)
	}
}

func TestANewRuleEscalatesEvenAtTheSameSeverity(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityHigh))
	r.now = t0.Add(time.Minute)
	out := r.raise(det("abcd1234", "new_asn", store.SeverityMedium))
	if out.Change != detect.Escalated || out.Incident.Severity != store.SeverityHigh {
		t.Fatalf("Outcome = %+v, want an escalation that keeps the highest severity", out)
	}
	if rules := out.Incident.Rules(); len(rules) != 2 || rules[0] != "new_country" || rules[1] != "new_asn" {
		t.Errorf("rules = %v", rules)
	}
	// Seeing a rule the incident already has, at a lower severity, is a repeat.
	r.now = t0.Add(2 * time.Minute)
	if out := r.raise(det("abcd1234", "new_asn", store.SeverityLow)); out.Change != detect.Repeated {
		t.Errorf("a lower severity of a known rule = %v, want a repeat", out.Change)
	}
}

func TestOneIncidentPerKeyAndEachKeyHasItsOwn(t *testing.T) {
	r := newRig(t)
	a := r.raise(det("abcd1234", "r1", store.SeverityLow))
	b := r.raise(det("beef5678", "r1", store.SeverityLow))
	a2 := r.raise(det("abcd1234", "r2", store.SeverityLow))
	if a.Incident.ID == b.Incident.ID || a2.Incident.ID != a.Incident.ID {
		t.Errorf("incidents %d, %d, %d: want one per key", a.Incident.ID, b.Incident.ID, a2.Incident.ID)
	}
}

func TestAFiringOnAQuietIncidentCountsAsAnEscalation(t *testing.T) {
	for _, state := range []string{store.IncidentOpen, store.IncidentAcknowledged} {
		t.Run(state, func(t *testing.T) {
			r := newRig(t)
			r.raise(det("abcd1234", "new_country", store.SeverityMedium))
			if state == store.IncidentAcknowledged {
				r.update(1, func(i *store.Incident) { i.State, i.AcknowledgedAt, i.AcknowledgedBy = state, t0, "owner" })
			}
			// Just under an hour of silence is not quiet.
			r.now = t0.Add(59 * time.Minute)
			if out := r.raise(det("abcd1234", "new_country", store.SeverityMedium)); out.Change != detect.Repeated {
				t.Fatalf("after 59 minutes: %v, want a repeat", out.Change)
			}
			// An hour from the last firing, it is quiet.
			r.now = r.now.Add(time.Hour)
			out := r.raise(det("abcd1234", "new_country", store.SeverityMedium))
			if out.Change != detect.Escalated || out.Incident.State != state {
				t.Fatalf("after an hour of silence: %+v, want an escalation that keeps the state %s", out, state)
			}
			if n := len(r.alerts.got()); n != 2 {
				t.Errorf("%d alerts, want the opening and the escalation", n)
			}
		})
	}
}

func TestAcknowledgingStopsRepeatsButEscalationsStillAlert(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	r.update(1, func(i *store.Incident) {
		i.State, i.AcknowledgedAt, i.AcknowledgedBy = store.IncidentAcknowledged, t0, "owner"
	})
	r.now = t0.Add(time.Minute)
	if out := r.raise(det("abcd1234", "new_country", store.SeverityMedium)); out.Change != detect.Repeated {
		t.Errorf("a repeat on an acknowledged incident = %v", out.Change)
	}
	if out := r.raise(det("abcd1234", "new_asn", store.SeverityMedium)); out.Change != detect.Escalated || out.Incident.State != store.IncidentAcknowledged {
		t.Errorf("a new rule on an acknowledged incident = %+v", out)
	}
	if n := len(r.alerts.got()); n != 2 {
		t.Errorf("%d alerts, want 2", n)
	}
}

func TestAResolvedIncidentIsNotReopened(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	r.update(1, func(i *store.Incident) {
		i.State, i.Resolution, i.ResolvedAt, i.ResolvedBy = store.IncidentResolved, store.ResolutionConfirmed, t0, "owner"
	})
	r.now = t0.Add(time.Minute)
	out := r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	if out.Change != detect.Opened || out.Incident.ID != 2 {
		t.Fatalf("Outcome = %+v, want a new incident, 2", out)
	}
	if got := r.incident(1); got.State != store.IncidentResolved || got.Detections != 1 {
		t.Errorf("the resolved incident changed: %+v", got)
	}
}

func TestASilentDetectionRecordsWithoutAlerting(t *testing.T) {
	r := newRig(t)
	d := det("cafe0001", "canary", store.SeverityCritical)
	d.Silent = true
	out := r.raise(d)
	if out.Change != detect.Opened || out.Incident.Severity != store.SeverityCritical || out.Incident.AlertedSeverity != "" {
		t.Fatalf("Outcome = %+v", out)
	}
	if n := len(r.alerts.got()); n != 0 || r.wakes() != 0 {
		t.Errorf("a silent detection queued %d alerts and woke the dispatcher %d times", n, r.wakes())
	}
	if es := r.audit(1); len(es) != 1 || es[0].Action != "incident.open" {
		t.Errorf("audit = %+v: a silent detection is still audited", es)
	}
}

func TestDetectionsAreRecordedUpToABoundAndTheCounterGoesOn(t *testing.T) {
	r := newRig(t)
	for range detect.MaxDetectionsPerIncident + 25 {
		r.raise(det("abcd1234", "new_country", store.SeverityMedium))
	}
	if got := r.incident(1); got.Detections != detect.MaxDetectionsPerIncident+25 {
		t.Errorf("counter = %d, want %d", got.Detections, detect.MaxDetectionsPerIncident+25)
	}
	if n := len(r.detections(1)); n != detect.MaxDetectionsPerIncident {
		t.Errorf("%d detection rows, want %d", n, detect.MaxDetectionsPerIncident)
	}
}

func TestEscalationsAreRecordedEvenPastTheBound(t *testing.T) {
	r := newRig(t)
	for range detect.MaxDetectionsPerIncident + 5 {
		r.raise(det("abcd1234", "new_country", store.SeverityLow))
	}
	r.raise(det("abcd1234", "new_asn", store.SeverityHigh))
	ds := r.detections(1)
	if last := ds[len(ds)-1]; last.Rule != "new_asn" {
		t.Errorf("the escalation was not recorded: last row %+v", last)
	}
}

func TestRaiseRefusesWhatIsNotADetection(t *testing.T) {
	r := newRig(t)
	for name, d := range map[string]detect.Detection{
		"no rule":          {KeyID: "abcd1234", Severity: store.SeverityLow, At: t0},
		"no key":           {Rule: "r", Severity: store.SeverityLow, At: t0},
		"an unknown level": {KeyID: "abcd1234", Rule: "r", Severity: "urgent", At: t0},
		"a missing key":    {KeyID: "nokey000", Rule: "r", Severity: store.SeverityLow, At: t0},
	} {
		if _, err := r.engine.RaiseNow(context.Background(), d); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAFailedAlertRollsTheWholeDetectionBack(t *testing.T) {
	r := newRig(t)
	r.alerts.fail = errors.New("outbox full")
	if _, err := r.engine.RaiseNow(context.Background(), det("abcd1234", "r", store.SeverityLow)); err == nil {
		t.Fatal("Raise hid the alert's failure")
	}
	var n int
	r.store.Do(context.Background(), func(tx *store.Tx) error {
		is, _ := tx.Incidents(store.IncidentFilter{})
		n = len(is)
		return nil
	})
	if n != 0 {
		t.Errorf("%d incidents survived an alert that failed, want none: an incident and its alert commit together", n)
	}
}

func TestSweepExpiresAnUnacknowledgedIncidentQuietForSevenDays(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityHigh))
	r.now = t0.Add(7*24*time.Hour - time.Second)
	if n, err := r.engine.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("Sweep just before seven days = %d, %v", n, err)
	}
	r.now = t0.Add(7 * 24 * time.Hour)
	if n, err := r.engine.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("Sweep at seven days = %d, %v", n, err)
	}
	got := r.incident(1)
	if got.State != store.IncidentResolved || got.Resolution != store.ResolutionExpired || got.ResolvedBy != "system:detect" || !got.ResolvedAt.Equal(r.now) {
		t.Errorf("incident = %+v", got)
	}
	es := r.audit(1)
	if len(es) != 2 || es[1].Action != "incident.expire" || es[1].Actor != "system:detect" {
		t.Errorf("audit = %+v, want an incident.expire by system:detect", es)
	}
	if n := len(r.alerts.got()); n != 1 {
		t.Errorf("expiry queued an alert: %d alerts", n)
	}
	// The key may open a new incident.
	if out := r.raise(det("abcd1234", "new_country", store.SeverityHigh)); out.Change != detect.Opened || out.Incident.ID != 2 {
		t.Errorf("after expiry: %+v", out)
	}
}

func TestSweepNeverExpiresAnAcknowledgedOrACriticalIncident(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityHigh))
	r.update(1, func(i *store.Incident) {
		i.State, i.AcknowledgedAt, i.AcknowledgedBy = store.IncidentAcknowledged, t0, "owner"
	})
	d := det("cafe0001", "canary", store.SeverityCritical)
	d.Silent = true
	r.raise(d)
	r.now = t0.Add(400 * 24 * time.Hour)
	if n, err := r.engine.Sweep(context.Background()); err != nil || n != 0 {
		t.Errorf("Sweep = %d, %v, want nothing expired", n, err)
	}
	for _, id := range []int64{1, 2} {
		if got := r.incident(id); got.State == store.IncidentResolved {
			t.Errorf("incident %d expired: %+v", id, got)
		}
	}
}

func TestARepeatKeepsAnIncidentFromExpiring(t *testing.T) {
	r := newRig(t)
	r.raise(det("abcd1234", "new_country", store.SeverityHigh))
	r.now = t0.Add(6 * 24 * time.Hour)
	r.raise(det("abcd1234", "new_country", store.SeverityHigh))
	r.now = t0.Add(8 * 24 * time.Hour) // 2 days since it last fired
	if n, _ := r.engine.Sweep(context.Background()); n != 0 {
		t.Error("an incident that fired two days ago expired")
	}
}

func TestRunSweepExpiresOnItsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		base := time.Now()
		eng := detect.New(detect.Options{Store: st, Now: time.Now, SweepEvery: time.Hour})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { eng.RunSweep(ctx); close(done) }()
		defer func() { cancel(); <-done }()
		// A key and a quiet incident for it, opened now.
		seedKey(t, st, base)
		if _, err := eng.RaiseNow(context.Background(), det("abcd1234", "new_country", store.SeverityHigh)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(7*24*time.Hour - time.Minute)
		synctest.Wait()
		if got := state(t, st); got != store.IncidentOpen {
			t.Fatalf("state before seven days = %s", got)
		}
		time.Sleep(2 * time.Hour) // the next sweep after the seven days
		synctest.Wait()
		if got := state(t, st); got != store.IncidentResolved {
			t.Fatalf("state after the sweep = %s, want resolved", got)
		}
	})
}

func seedKey(t *testing.T, st *store.Store, at time.Time) {
	t.Helper()
	err := st.Do(context.Background(), func(tx *store.Tx) error {
		if err := tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: at}); err != nil {
			return err
		}
		u, err := tx.CreateUpstream(store.Upstream{Name: "openai", Preset: "openai", PresetVersion: 1, Profile: "openai", Host: "api.openai.com", Port: 443,
			TLS: true, PathAllowlistJSON: `["/v1/"]`, PriceMultiplierPPM: 1_000_000, CreatedAt: at, UpdatedAt: at})
		if err != nil {
			return err
		}
		if _, err := tx.CreateCredential(store.Credential{ID: "cr_1", UpstreamID: u.ID, Name: "main", Ciphertext: []byte("c"), Nonce: []byte("n"),
			DataKeyID: "dek_1", Last4: "abcd", CreatedAt: at}); err != nil {
			return err
		}
		_, err = tx.CreateKey(store.VirtualKey{ID: "abcd1234", Name: "app", CredentialID: "cr_1", PolicyJSON: `{}`, PolicyVersion: 1, TagsJSON: `[]`, CreatedAt: at},
			[]byte("01234567890123456789012345678901"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func state(t *testing.T, st *store.Store) string {
	t.Helper()
	var s string
	if err := st.Do(context.Background(), func(tx *store.Tx) error {
		i, err := tx.Incident(1)
		s = i.State
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestQuietIsOneHourFromTheLastFiring(t *testing.T) {
	inc := store.Incident{LastFiredAt: t0}
	if detect.Quiet(inc, t0.Add(59*time.Minute+59*time.Second), time.Hour) || !detect.Quiet(inc, t0.Add(time.Hour), time.Hour) {
		t.Error("an incident is quiet from exactly one hour after it last fired")
	}
	if detect.Quiet(store.Incident{State: store.IncidentResolved, LastFiredAt: t0}, t0.Add(48*time.Hour), time.Hour) {
		t.Error("a resolved incident is not quiet: it is over")
	}
}

func TestKnownSourcesMatchByKind(t *testing.T) {
	sources := []store.KnownSource{
		{ID: 1, Kind: store.SourceCIDR, Value: "203.0.113.0/24", Label: "runners"},
		{ID: 2, Kind: store.SourceCountry, Value: "DE", Label: "office"},
		{ID: 3, Kind: store.SourceASN, Value: "3320", Label: "telekom"},
		{ID: 4, Kind: store.SourceClient, Value: "curl/8", Label: "ci"},
		{ID: 5, Scope: "beef5678", Kind: store.SourceCountry, Value: "FR", Label: "other key"},
		{ID: 6, Kind: store.SourceCIDR, Value: "2001:db8::/32", Label: "v6"},
	}
	cases := []struct {
		name string
		kind string // the kind of place that is new
		o    detect.Origin
		want int64
	}{
		{"an address in a CIDR", store.PlaceNetwork, detect.Origin{Addr: netip.MustParseAddr("203.0.113.77")}, 1},
		{"the same address mapped into IPv6", store.PlaceNetwork, detect.Origin{Addr: netip.MustParseAddr("::ffff:203.0.113.77")}, 1},
		{"an address outside", store.PlaceNetwork, detect.Origin{Addr: netip.MustParseAddr("203.0.114.1")}, 0},
		{"an IPv6 address in a v6 CIDR", store.PlaceNetwork, detect.Origin{Addr: netip.MustParseAddr("2001:db8:1::1")}, 6},
		{"a country", store.PlaceCountry, detect.Origin{Country: "DE"}, 2},
		{"another country", store.PlaceCountry, detect.Origin{Country: "US"}, 0},
		{"an ASN", store.PlaceASN, detect.Origin{ASN: 3320}, 3},
		{"another ASN", store.PlaceASN, detect.Origin{ASN: 1}, 0},
		{"a client", store.PlaceClient, detect.Origin{Client: "curl/8"}, 4},
		{"another client version", store.PlaceClient, detect.Origin{Client: "curl/7"}, 0},
		{"a source for another key", store.PlaceCountry, detect.Origin{Country: "FR"}, 0},
		{"nothing is known", store.PlaceCountry, detect.Origin{}, 0},
	}
	for _, c := range cases {
		got, ok := detect.MatchKnownPlace(sources, "abcd1234", false, c.kind, c.o)
		if c.want == 0 && ok || c.want != 0 && (!ok || got.ID != c.want) {
			t.Errorf("%s: got %+v, %v; want source %d", c.name, got, ok, c.want)
		}
	}
	if got, ok := detect.MatchKnownPlace(sources, "beef5678", false, store.PlaceCountry, detect.Origin{Country: "FR"}); !ok || got.ID != 5 {
		t.Errorf("a key's own source: %+v, %v", got, ok)
	}
}

// #89: a known source silences the place of its own kind, and only a CIDR, which
// says where the request came from, silences a request whatever is new in it.
func TestAKnownSourceSilencesOnlyThePlaceOfItsKindAndACIDRSilencesTheRequest(t *testing.T) {
	cidr := store.KnownSource{ID: 1, Kind: store.SourceCIDR, Value: "203.0.113.0/24"}
	country := store.KnownSource{ID: 2, Kind: store.SourceCountry, Value: "DE"}
	asn := store.KnownSource{ID: 3, Kind: store.SourceASN, Value: "3320"}
	client := store.KnownSource{ID: 4, Kind: store.SourceClient, Value: "curl/8"}
	all := []store.KnownSource{cidr, country, asn, client}
	in := detect.Origin{Addr: netip.MustParseAddr("203.0.113.77"), Country: "DE", ASN: 3320, Client: "curl/8"}
	out := detect.Origin{Addr: netip.MustParseAddr("198.51.100.1"), Country: "DE", ASN: 3320, Client: "curl/8"}
	for _, c := range []struct {
		name    string
		sources []store.KnownSource
		kind    string
		o       detect.Origin
		want    int64
	}{
		{"a CIDR silences a new country", all[:1], store.PlaceCountry, in, 1},
		{"a CIDR silences a new ASN", all[:1], store.PlaceASN, in, 1},
		{"a CIDR silences a new network", all[:1], store.PlaceNetwork, in, 1},
		{"a CIDR silences a new client", all[:1], store.PlaceClient, in, 1},
		{"an address outside the CIDR is not covered", all[:1], store.PlaceCountry, out, 0},
		{"a country silences a new country", all[1:2], store.PlaceCountry, out, 2},
		{"a country does not silence a new ASN", all[1:2], store.PlaceASN, out, 0},
		{"a country does not silence a new client", all[1:2], store.PlaceClient, out, 0},
		{"a country does not silence a new network", all[1:2], store.PlaceNetwork, out, 0},
		{"an ASN silences a new ASN", all[2:3], store.PlaceASN, out, 3},
		{"an ASN does not silence a new country", all[2:3], store.PlaceCountry, out, 0},
		{"an ASN does not silence a new network", all[2:3], store.PlaceNetwork, out, 0},
		{"a client silences a new client", all[3:], store.PlaceClient, out, 4},
		{"a client does not silence a new country", all[3:], store.PlaceCountry, out, 0},
		{"a client does not silence a new ASN", all[3:], store.PlaceASN, out, 0},
		{"a client does not silence a new network", all[3:], store.PlaceNetwork, out, 0},
		{"another country is not the source's", all[1:2], store.PlaceCountry, detect.Origin{Country: "FR"}, 0},
	} {
		got, ok := detect.MatchKnownPlace(c.sources, "abcd1234", false, c.kind, c.o)
		if c.want == 0 && ok || c.want != 0 && (!ok || got.ID != c.want) {
			t.Errorf("%s: got %+v, %v; want source %d", c.name, got, ok, c.want)
		}
	}
}

func TestAKnownSourceNeverSilencesACanary(t *testing.T) {
	sources := []store.KnownSource{
		{ID: 1, Kind: store.SourceCIDR, Value: "203.0.113.0/24"}, {ID: 2, Kind: store.SourceCountry, Value: "DE"},
		{ID: 3, Kind: store.SourceASN, Value: "3320"}, {ID: 4, Kind: store.SourceClient, Value: "curl/8"},
	}
	o := detect.Origin{Addr: netip.MustParseAddr("203.0.113.5"), Country: "DE", ASN: 3320, Client: "curl/8"}
	for _, kind := range []string{store.PlaceCountry, store.PlaceASN, store.PlaceNetwork, store.PlaceClient} {
		if _, ok := detect.MatchKnownPlace(sources, "abcd1234", false, kind, o); !ok {
			t.Fatalf("a standard key's known source did not match a new %s", kind)
		}
		if got, ok := detect.MatchKnownPlace(sources, "cafe0001", true, kind, o); ok {
			t.Errorf("a known source silenced a canary's %s: %+v (ADR 0013)", kind, got)
		}
	}
}

func TestAnIncidentAlertNeverNeedsAnAlerter(t *testing.T) {
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedKey(t, st, t0)
	eng := detect.New(detect.Options{Store: st, Now: func() time.Time { return t0 }})
	out, err := eng.RaiseNow(context.Background(), det("abcd1234", "r", store.SeverityLow))
	if err != nil || out.Change != detect.Opened {
		t.Fatalf("Raise without an alerter = %+v, %v", out, err)
	}
	if !strings.Contains(string(out.Change), "opened") {
		t.Errorf("change = %q", out.Change)
	}
}
