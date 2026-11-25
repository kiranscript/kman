package admin_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/detect"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

// withIncident is an install with two keys, app and batch, and an open incident
// for app: a new-country detection of medium severity from Germany. It is
// incident 1.
func withIncident(t *testing.T) (*install, admin.CreatedKey, admin.CreatedKey) {
	t.Helper()
	in, _ := withCredential(t)
	app := in.createKey(t, admin.CreateKeyRequest{Name: "app", Upstream: "openai"})
	batch := in.createKey(t, admin.CreateKeyRequest{Name: "batch", Upstream: "openai"})
	in.detect(t, app.Key.ID, "new_country", store.SeverityMedium, store.Evidence{
		Address: "203.0.113.7", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Client: "curl/8", Observed: "DE", Baseline: "US",
	})
	return in, app, batch
}

// detect raises a detection, as a rule does.
func (in *install) detect(t *testing.T, keyID, rule string, sev store.Severity, ev store.Evidence) detect.Outcome {
	t.Helper()
	e := detect.New(detect.Options{Store: in.store, Now: func() time.Time { return in.now }})
	out, err := e.RaiseNow(t.Context(), detect.Detection{KeyID: keyID, Rule: rule, Severity: sev, At: in.now, Evidence: ev})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (in *install) incident(t *testing.T, id int64) admin.Incident {
	t.Helper()
	d, err := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: id})
	if err != nil {
		t.Fatalf("GetIncident(%d) = %v", id, err)
	}
	return d.Incident
}

func TestAnIncidentIsListedAndShownWithItsDetectionsAndTimeline(t *testing.T) {
	in, app, _ := withIncident(t)
	in.now = in.now.Add(5 * time.Minute)
	in.detect(t, app.Key.ID, "new_asn", store.SeverityHigh, store.Evidence{ASN: 3320})
	in.now = in.now.Add(5 * time.Minute)
	if _, err := in.svc.AckIncident(as("owner"), admin.IncidentRef{ID: 1}); err != nil {
		t.Fatal(err)
	}

	list, err := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{})
	if err != nil || len(list.Incidents) != 1 || list.More {
		t.Fatalf("ListIncidents = %+v (%v)", list, err)
	}
	i := list.Incidents[0]
	if i.ID != 1 || i.KeyID != app.Key.ID || i.KeyName != "app" || i.State != "acknowledged" || i.Severity != "high" || i.Quiet ||
		len(i.Rules) != 2 || i.Rules[0] != "new_country" || i.DetectionCount != 2 || i.AcknowledgedBy != "owner" || i.AcknowledgedAt == nil ||
		i.ResolvedAt != nil || i.Resolution != "" || !i.OpenedAt.Equal(t0) || !i.LastFiredAt.Equal(t0.Add(5*time.Minute)) {
		t.Errorf("incident = %+v", i)
	}

	d, err := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Detections) != 2 || d.Detections[0].Rule != "new_country" || d.Detections[1].Severity != "high" || d.Detections[0].Evidence.Country != "DE" ||
		d.Detections[0].Evidence.Client != "curl/8" || d.Detections[0].Evidence.Observed != "DE" {
		t.Errorf("detections = %+v", d.Detections)
	}
	var acts []string
	for _, a := range d.Actions {
		acts = append(acts, summary(a))
		if a.IncidentID != 1 {
			t.Errorf("action %+v does not carry the incident id", a)
		}
	}
	want := []string{"system:detect incident.open incident/1", "system:detect incident.escalate incident/1", "owner incident.acknowledge incident/1"}
	if strings.Join(acts, "|") != strings.Join(want, "|") {
		t.Errorf("actions = %v, want %v", acts, want)
	}
	if _, err := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 99}); code(err) != admin.CodeNotFound {
		t.Errorf("GetIncident(99) = %v, want not found", err)
	}
}

func TestIncidentAddressesAreWithheldUnlessExposeClientAddressesIsOn(t *testing.T) {
	in, app, _ := withIncident(t)
	in.detect(t, app.Key.ID, "new_network", store.SeverityLow, store.Evidence{Address: "198.51.100.9", Network: "198.51.100.0/24"})

	d, err := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !d.AddressesWithheld {
		t.Error("the detail does not say it withheld addresses")
	}
	for _, det := range d.Detections {
		if det.Evidence.Address != "" || det.Evidence.Network != "" {
			t.Errorf("evidence gave out an address or network: %+v", det.Evidence)
		}
	}
	if !d.Detections[0].Evidence.AddressWithheld || d.Detections[0].Evidence.Country != "DE" || d.Detections[0].Evidence.ASN != 3320 {
		t.Errorf("evidence = %+v, want the address withheld and the country and ASN shown", d.Detections[0].Evidence)
	}
	for _, c := range d.Candidates {
		if c.Value == "203.0.113.0/24" || c.Value == "198.51.100.0/24" {
			t.Errorf("a candidate gave out a network: %+v", c)
		}
	}

	in.exposeClients(t, true)
	d, err = in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if d.AddressesWithheld || d.Detections[0].Evidence.Address != "203.0.113.7" || d.Detections[1].Evidence.Network != "198.51.100.0/24" {
		t.Errorf("with the setting on, evidence = %+v", d.Detections)
	}
}

func TestIncidentsFilterByOpenKeyAndPage(t *testing.T) {
	in, app, batch := withIncident(t)
	in.now = in.now.Add(time.Minute)
	in.detect(t, batch.Key.ID, "new_asn", store.SeverityLow, store.Evidence{ASN: 1})
	if _, err := in.svc.ResolveIncident(as("owner"), admin.ResolveIncidentRequest{ID: 1}); err != nil {
		t.Fatal(err)
	}
	all, _ := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{})
	open, _ := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{Open: true})
	forApp, _ := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{Key: app.Key.ID})
	page, err := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{Limit: 1})
	if len(all.Incidents) != 2 || all.Incidents[0].ID != 2 || len(open.Incidents) != 1 || open.Incidents[0].ID != 2 ||
		len(forApp.Incidents) != 1 || forApp.Incidents[0].ID != 1 || err != nil || len(page.Incidents) != 1 || !page.More {
		t.Errorf("all %+v\nopen %+v\nforApp %+v\npage %+v (%v)", all, open, forApp, page, err)
	}
	next, _ := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{Before: 2, Limit: 5})
	if len(next.Incidents) != 1 || next.Incidents[0].ID != 1 || next.More {
		t.Errorf("next page = %+v", next)
	}
	if _, err := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{Limit: 5000}); code(err) != admin.CodeInvalid {
		t.Errorf("a limit of 5000 = %v, want invalid", err)
	}
	if _, err := in.svc.ListIncidents(as("owner"), admin.IncidentQuery{Key: "nokey000"}); code(err) != admin.CodeNotFound {
		t.Errorf("an unknown key = %v, want not found", err)
	}
}

func TestAQuietIncidentSaysSo(t *testing.T) {
	in, _, _ := withIncident(t)
	in.now = in.now.Add(59 * time.Minute)
	if in.incident(t, 1).Quiet {
		t.Error("quiet after 59 minutes")
	}
	in.now = in.now.Add(time.Minute)
	if !in.incident(t, 1).Quiet {
		t.Error("not quiet after an hour")
	}
}

func TestAcknowledgingIsAuditedWithTheIncidentIDAndIsIdempotent(t *testing.T) {
	in, _, _ := withIncident(t)
	in.now = in.now.Add(time.Minute)
	got, err := in.svc.AckIncident(as("owner"), admin.IncidentRef{ID: 1})
	if err != nil || got.State != "acknowledged" || got.AcknowledgedBy != "owner" || got.AcknowledgedAt == nil || !got.AcknowledgedAt.Equal(in.now) {
		t.Fatalf("Ack = %+v (%v)", got, err)
	}
	es := in.latest(t, 1)
	if len(es) != 1 || es[0].Action != "incident.acknowledge" || es[0].IncidentID != 1 || es[0].Actor != "owner" || es[0].TargetID != "1" {
		t.Errorf("audit = %+v", es)
	}
	before := len(in.allAudit(t))
	if _, err := in.svc.AckIncident(as("owner"), admin.IncidentRef{ID: 1}); err != nil {
		t.Errorf("acknowledging twice: %v", err)
	}
	if len(in.allAudit(t)) != before {
		t.Error("a second acknowledgement wrote an audit entry")
	}
	if _, err := in.svc.AckIncident(as("owner"), admin.IncidentRef{ID: 99}); code(err) != admin.CodeNotFound {
		t.Errorf("Ack(99) = %v", err)
	}
	if _, err := in.svc.AckIncident(admin.WithCaller(t.Context(), admin.Caller{}), admin.IncidentRef{ID: 1}); code(err) != admin.CodeUnauthorized {
		t.Errorf("Ack with no caller = %v", err)
	}
}

func TestResolvingMarksTheResolutionAndWhoAndFreesTheKeyForANewIncident(t *testing.T) {
	in, app, _ := withIncident(t)
	in.now = in.now.Add(time.Minute)
	got, err := in.svc.ResolveIncident(as("owner"), admin.ResolveIncidentRequest{ID: 1, As: "false-positive"})
	if err != nil || got.State != "resolved" || got.Resolution != "false_positive" || got.ResolvedBy != "owner" || got.ResolvedAt == nil {
		t.Fatalf("Resolve = %+v (%v)", got, err)
	}
	es := in.latest(t, 1)
	if es[0].Action != "incident.resolve" || es[0].IncidentID != 1 {
		t.Errorf("audit = %+v", es[0])
	}
	// Resolving the same way again is no change; another way is a conflict.
	if _, err := in.svc.ResolveIncident(as("owner"), admin.ResolveIncidentRequest{ID: 1, As: "false_positive"}); err != nil {
		t.Errorf("resolving again the same way: %v", err)
	}
	if _, err := in.svc.ResolveIncident(as("owner"), admin.ResolveIncidentRequest{ID: 1, As: "confirmed"}); code(err) != admin.CodeConflict {
		t.Errorf("resolving another way = %v, want conflict", err)
	}
	if _, err := in.svc.AckIncident(as("owner"), admin.IncidentRef{ID: 1}); code(err) != admin.CodeConflict {
		t.Errorf("acknowledging a resolved incident = %v, want conflict", err)
	}
	if out := in.detect(t, app.Key.ID, "new_country", store.SeverityMedium, store.Evidence{}); out.Incident.ID != 2 {
		t.Errorf("the next detection is incident %d, want a new one, 2", out.Incident.ID)
	}
}

func TestResolveDefaultsToConfirmedAndRefusesWhatOperatorsCannotChoose(t *testing.T) {
	in, _, _ := withIncident(t)
	for _, as_ := range []string{"expired", "whatever"} {
		if _, err := in.svc.ResolveIncident(as("owner"), admin.ResolveIncidentRequest{ID: 1, As: as_}); code(err) != admin.CodeInvalid {
			t.Errorf("resolve as %q = %v, want invalid", as_, err)
		}
	}
	got, err := in.svc.ResolveIncident(as("owner"), admin.ResolveIncidentRequest{ID: 1})
	if err != nil || got.Resolution != "confirmed" {
		t.Errorf("resolve with no --as = %+v (%v), want confirmed", got, err)
	}
}

func TestThisWasMeResolvesAsAFalsePositiveAndAddsNothingUnlessAsked(t *testing.T) {
	in, _, _ := withIncident(t)
	got, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1})
	if err != nil || got.Incident.Resolution != "false_positive" || got.Incident.State != "resolved" || len(got.Added) != 0 {
		t.Fatalf("ThisWasMe = %+v (%v)", got, err)
	}
	es := in.latest(t, 1)
	if es[0].Action != "incident.this_was_me" || es[0].IncidentID != 1 {
		t.Errorf("audit = %+v", es[0])
	}
	if ks, _ := in.svc.ListKnownSources(as("owner"), admin.KnownSourceQuery{}); len(ks.KnownSources) != 0 {
		t.Errorf("known sources = %+v, want none", ks)
	}
}

func TestThisWasMeAddsTheIncidentsPlacesAsKnownSourcesForTheKey(t *testing.T) {
	in, app, _ := withIncident(t)
	in.exposeClients(t, true)
	got, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1, AddKnownSources: true})
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, k := range got.Added {
		added = append(added, k.Kind+":"+k.Value)
		if k.Scope != app.Key.ID || k.CreatedBy != "owner" || k.Label == "" || !strings.Contains(k.Label, "incident 1") {
			t.Errorf("known source = %+v", k)
		}
	}
	for _, want := range []string{"country:DE", "asn:3320", "client:curl/8", "cidr:203.0.113.0/24"} {
		found := false
		for _, a := range added {
			found = found || a == want
		}
		if !found {
			t.Errorf("added %v, lacks %s", added, want)
		}
	}
	// Each addition is audited with the incident id, besides the resolution.
	var adds, resolves int
	for _, e := range in.latest(t, 10) {
		switch e.Action {
		case "known_source.create":
			adds++
			if e.IncidentID != 1 {
				t.Errorf("known source audit entry has incident %d", e.IncidentID)
			}
		case "incident.this_was_me":
			resolves++
		}
	}
	if adds != len(got.Added) || resolves != 1 {
		t.Errorf("%d known_source.create entries for %d added, %d resolutions", adds, len(got.Added), resolves)
	}
}

// Past MaxDetectionsPerIncident detect records only escalations, so an
// escalation late in a busy incident is past the oldest 100 rows. The detail
// still shows it, and this was me still offers its places (#110).
func TestALateEscalationInABusyIncidentIsShownAndOfferedAsAKnownSource(t *testing.T) {
	in, app, _ := withIncident(t)
	in.exposeClients(t, true)
	repeat := store.Evidence{Address: "203.0.113.7", Country: "DE", ASN: 3320, Client: "curl/8"}
	for range detect.MaxDetectionsPerIncident + 20 {
		in.detect(t, app.Key.ID, "new_country", store.SeverityMedium, repeat)
	}
	in.detect(t, app.Key.ID, "new_asn", store.SeverityHigh, store.Evidence{Address: "198.51.100.9", Country: "BR", ASN: 28573, ASOrg: "Claro", Client: "evil/1"})
	d, err := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	n := len(d.Detections)
	if n != 100 || d.Detections[0].Rule != "new_country" || d.Detections[n-1].Rule != "new_asn" || d.Detections[n-1].Severity != "high" {
		t.Fatalf("%d detections shown, first %+v, last %+v: want 100 with the late escalation last", n, d.Detections[0], d.Detections[n-1])
	}
	if d.DetectionCount != int64(detect.MaxDetectionsPerIncident)+22 {
		t.Errorf("detection_count = %d", d.DetectionCount)
	}
	var offered []string
	for _, c := range d.Candidates {
		offered = append(offered, c.Kind+":"+c.Value)
	}
	if !strings.Contains(strings.Join(offered, " "), "country:BR") {
		t.Errorf("candidates %v lack the escalation's country", offered)
	}
	got, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1, AddKnownSources: true})
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, k := range got.Added {
		added = append(added, k.Kind+":"+k.Value)
	}
	for _, want := range []string{"country:DE", "country:BR", "asn:28573", "client:evil/1", "cidr:198.51.100.0/24"} {
		if !strings.Contains(" "+strings.Join(added, " ")+" ", " "+want+" ") {
			t.Errorf("added %v, lacks %s", added, want)
		}
	}
}

func TestThisWasMeOnlyAddsNetworksWhenAddressesAreExposed(t *testing.T) {
	in, _, _ := withIncident(t)
	got, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1, AddKnownSources: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.WithheldNetworks != 1 {
		t.Errorf("WithheldNetworks = %d, want 1", got.WithheldNetworks)
	}
	for _, k := range got.Added {
		if k.Kind == "cidr" {
			t.Errorf("added a network while addresses are withheld: %+v", k)
		}
	}
	if len(got.Added) != 3 {
		t.Errorf("added %+v, want the country, the ASN and the client", got.Added)
	}
}

func TestThisWasMeCanBeInstallWideAndRestrictedToKinds(t *testing.T) {
	in, _, _ := withIncident(t)
	got, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1, AddKnownSources: true, InstallWide: true, Kinds: []string{"country"}, Label: "the Berlin office"})
	if err != nil || len(got.Added) != 1 || got.Added[0].Kind != "country" || got.Added[0].Scope != "" || got.Added[0].Label != "the Berlin office" {
		t.Fatalf("ThisWasMe = %+v (%v)", got, err)
	}
}

func TestThisWasMeSkipsWhatIsAlreadyKnown(t *testing.T) {
	in, _, _ := withIncident(t)
	if _, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "country", Value: "DE", Label: "office"}); err != nil {
		t.Fatal(err)
	}
	got, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1, AddKnownSources: true, Kinds: []string{"country"}})
	if err != nil || len(got.Added) != 0 || got.AlreadyKnown != 1 {
		t.Errorf("ThisWasMe = %+v (%v), want the country already known", got, err)
	}
}

func TestThisWasMeNeverAddsKnownSourcesForACanary(t *testing.T) {
	in, _ := withCredential(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "the wiki"})
	in.detect(t, c.Canary.ID, "canary", store.SeverityCritical, store.Evidence{Country: "DE"})
	if _, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1, AddKnownSources: true}); code(err) != admin.CodeConflict {
		t.Fatalf("this was me with known sources for a canary = %v, want conflict", err)
	}
	if got := in.incident(t, 1); got.State == "resolved" {
		t.Error("the refused request still resolved the incident")
	}
	// The operator who tested their own canary may still resolve it.
	got, err := in.svc.ThisWasMe(as("owner"), admin.ThisWasMeRequest{ID: 1})
	if err != nil || got.Incident.Resolution != "false_positive" {
		t.Errorf("this was me on a canary = %+v (%v)", got, err)
	}
	d, _ := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1})
	if len(d.Candidates) != 0 {
		t.Errorf("a canary incident offers candidates: %+v", d.Candidates)
	}
}

func TestThisWasMeNeedsAddKnownSourcesForTheOptionsThatBelongToIt(t *testing.T) {
	in, _, _ := withIncident(t)
	for _, req := range []admin.ThisWasMeRequest{
		{ID: 1, InstallWide: true}, {ID: 1, Kinds: []string{"country"}}, {ID: 1, Label: "x"},
		{ID: 1, AddKnownSources: true, Kinds: []string{"planet"}},
	} {
		if _, err := in.svc.ThisWasMe(as("owner"), req); code(err) != admin.CodeInvalid {
			t.Errorf("ThisWasMe(%+v) = %v, want invalid", req, err)
		}
	}
	if got := in.incident(t, 1); got.State == "resolved" {
		t.Error("a refused request resolved the incident")
	}
}

func TestKnownSourcesAreAddedListedAndRemovedWithAudit(t *testing.T) {
	in, app, _ := withIncident(t)
	wide, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "cidr", Value: "203.0.113.0/24", Label: "CI runners"})
	if err != nil || wide.ID == 0 || wide.Scope != "" || wide.Kind != "cidr" || wide.Value != "203.0.113.0/24" || wide.CreatedBy != "owner" {
		t.Fatalf("AddKnownSource = %+v (%v)", wide, err)
	}
	perKey, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "asn", Value: "AS3320", Label: "Telekom", Key: app.Key.ID})
	if err != nil || perKey.Value != "3320" || perKey.Scope != app.Key.ID || perKey.KeyName != "app" {
		t.Fatalf("AddKnownSource = %+v (%v)", perKey, err)
	}
	all, err := in.svc.ListKnownSources(as("owner"), admin.KnownSourceQuery{})
	if err != nil || len(all.KnownSources) != 2 || all.KnownSources[0].ID != wide.ID {
		t.Errorf("list = %+v (%v)", all, err)
	}
	if _, err := in.svc.ListKnownSources(as("owner"), admin.KnownSourceQuery{Key: "nokey000"}); code(err) != admin.CodeNotFound {
		t.Errorf("listing for an unknown key = %v, want not found", err)
	}
	if es := in.latest(t, 2); es[0].Action != "known_source.create" || es[1].Action != "known_source.create" || es[0].TargetKind != "known_source" {
		t.Errorf("audit = %+v", es)
	}
	removed, err := in.svc.RemoveKnownSource(as("owner"), admin.KnownSourceRef{ID: wide.ID})
	if err != nil || removed.ID != wide.ID {
		t.Fatalf("Remove = %+v (%v)", removed, err)
	}
	if es := in.latest(t, 1); es[0].Action != "known_source.delete" || !strings.Contains(string(es[0].Before), "203.0.113.0/24") {
		t.Errorf("audit = %+v", es[0])
	}
	if _, err := in.svc.RemoveKnownSource(as("owner"), admin.KnownSourceRef{ID: wide.ID}); code(err) != admin.CodeNotFound {
		t.Errorf("removing twice = %v, want not found", err)
	}
	if _, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "asn", Value: "3320", Label: "again", Key: app.Key.ID}); code(err) != admin.CodeConflict {
		t.Errorf("adding the same twice = %v, want conflict", err)
	}
}

func TestAKnownSourceIsValidatedAndBoundedSoItCannotSilenceEverything(t *testing.T) {
	in, app, _ := withIncident(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"})
	bad := map[string]admin.AddKnownSourceRequest{
		"no label":                    {Kind: "country", Value: "DE"},
		"a long label":                {Kind: "country", Value: "DE", Label: strings.Repeat("x", 65)},
		"a control character":         {Kind: "country", Value: "DE", Label: "a\nb"},
		"an unknown kind":             {Kind: "planet", Value: "DE", Label: "x"},
		"every address":               {Kind: "cidr", Value: "0.0.0.0/0", Label: "x"},
		"an IPv4 /15":                 {Kind: "cidr", Value: "10.0.0.0/15", Label: "x"},
		"an IPv6 /31":                 {Kind: "cidr", Value: "2001:db8::/31", Label: "x"},
		"every IPv6 address":          {Kind: "cidr", Value: "::/0", Label: "x"},
		"IPv4 written as IPv6":        {Kind: "cidr", Value: "::ffff:10.0.0.0/120", Label: "x"},
		"not a network":               {Kind: "cidr", Value: "banana", Label: "x"},
		"a country of three letters":  {Kind: "country", Value: "DEU", Label: "x"},
		"a country in lower case":     {Kind: "country", Value: "de", Label: "x"},
		"ASN 0":                       {Kind: "asn", Value: "0", Label: "x"},
		"an ASN past 32 bits":         {Kind: "asn", Value: "4294967296", Label: "x"},
		"an ASN that is not a number": {Kind: "asn", Value: "ASx", Label: "x"},
		"the client none":             {Kind: "client", Value: "none", Label: "x"},
		"the client unknown":          {Kind: "client", Value: "unknown", Label: "x"},
		"a client with a space":       {Kind: "client", Value: "my app/1", Label: "x"},
		"a client that is an address": {Kind: "client", Value: "203.0.113.7", Label: "x"},
		"a missing value":             {Kind: "client", Value: "", Label: "x"},
		"an unknown key":              {Kind: "country", Value: "DE", Label: "x", Key: "nokey000"},
	}
	for name, req := range bad {
		if _, err := in.svc.AddKnownSource(as("owner"), req); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	// A canary has no known sources: they never apply to one.
	if _, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "country", Value: "DE", Label: "x", Key: c.Canary.ID}); code(err) != admin.CodeConflict {
		t.Errorf("a known source for a canary = %v, want conflict", err)
	}
	good := map[string]struct{ in, want string }{
		"an IPv4 /16":           {"10.1.0.0/16", "10.1.0.0/16"},
		"an unmasked network":   {"203.0.113.77/24", "203.0.113.0/24"},
		"an IPv6 /32":           {"2001:db8::/32", "2001:db8::/32"},
		"a bare address":        {"203.0.113.9", "203.0.113.9/32"},
		"a bare IPv6 address":   {"2001:db8::1", "2001:db8::1/128"},
		"a mapped bare address": {"::ffff:203.0.113.10", "203.0.113.10/32"},
	}
	for name, c := range good {
		got, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "cidr", Value: c.in, Label: "x"})
		if err != nil || got.Value != c.want {
			t.Errorf("%s: %q = %+v (%v), want %q", name, c.in, got, err, c.want)
		}
	}
	for in_, want := range map[string]string{"AS64496": "64496", "as64497": "64497", "64498": "64498"} {
		got, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "asn", Value: in_, Label: "x", Key: app.Key.ID})
		if err != nil || got.Value != want {
			t.Errorf("ASN %q = %+v (%v), want %q", in_, got, err, want)
		}
	}
	if got, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "client", Value: "OpenAI-Python/3", Label: "x"}); err != nil || got.Value != "openai-python/3" {
		t.Errorf("client = %+v (%v), want it lower-cased as events record it", got, err)
	}
	// Every client an event can record can be a known source, the longest too,
	// and nothing longer (#116).
	for _, ua := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40) + "/1234", "Vendor/Language 1.2.3"} {
		client := events.ClientOf(ua)
		if got, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "client", Value: client, Label: "x"}); err != nil || got.Value != client {
			t.Errorf("the recorded client %q as a known source = %+v (%v)", client, got, err)
		}
	}
	if _, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "client", Value: strings.Repeat("c", events.MaxClientLength+1), Label: "x"}); err == nil {
		t.Error("a client longer than events record was accepted")
	}
}

func TestAScopeHoldsAtMostTwoHundredKnownSources(t *testing.T) {
	in, _, _ := withIncident(t)
	for i := range 200 {
		if _, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "asn", Value: strconv.Itoa(i + 1), Label: "x"}); err != nil {
			t.Fatalf("known source %d: %v", i, err)
		}
	}
	if _, err := in.svc.AddKnownSource(as("owner"), admin.AddKnownSourceRequest{Kind: "asn", Value: "1000", Label: "x"}); code(err) != admin.CodeConflict {
		t.Errorf("the 201st known source = %v, want conflict", err)
	}
}

// --incident on the containment commands (#88, #15).

func TestDisablingWithAnIncidentLeavesItOpenAndAuditsTheIDOnTheTimeline(t *testing.T) {
	in, app, _ := withIncident(t)
	got, err := in.svc.DisableKey(as("owner"), admin.ContainKeyRequest{ID: app.Key.ID, IncidentID: 1})
	if err != nil || got.Status != "disabled" {
		t.Fatalf("DisableKey = %+v (%v)", got, err)
	}
	if i := in.incident(t, 1); i.State != "open" {
		t.Errorf("incident state = %s, want it left open", i.State)
	}
	es := in.latest(t, 1)
	if es[0].Action != "key.disable" || es[0].IncidentID != 1 || es[0].TargetID != app.Key.ID {
		t.Errorf("audit = %+v, want key.disable carrying incident 1", es[0])
	}
	d, _ := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1})
	if last := d.Actions[len(d.Actions)-1]; last.Action != "key.disable" {
		t.Errorf("the timeline ends with %s, want key.disable", last.Action)
	}
}

func TestRotatingRevokingAndCompromiseWithAnIncidentResolveItAsConfirmed(t *testing.T) {
	t.Run("rotate", func(t *testing.T) {
		in, app, _ := withIncident(t)
		if _, err := in.svc.RotateKey(as("owner"), admin.RotateKeyRequest{ID: app.Key.ID, IncidentID: 1}); err != nil {
			t.Fatal(err)
		}
		i := in.incident(t, 1)
		if i.State != "resolved" || i.Resolution != "confirmed" || i.ResolvedBy != "owner" {
			t.Errorf("incident = %+v", i)
		}
		actions := map[string]int64{}
		for _, e := range in.latest(t, 4) {
			actions[e.Action] = e.IncidentID
		}
		if actions["key.rotate"] != 1 || actions["incident.resolve"] != 1 {
			t.Errorf("audit actions = %v, want key.rotate and incident.resolve to carry incident 1", actions)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		in, app, _ := withIncident(t)
		if _, err := in.svc.RevokeKey(as("owner"), admin.ContainKeyRequest{ID: app.Key.ID, IncidentID: 1}); err != nil {
			t.Fatal(err)
		}
		if i := in.incident(t, 1); i.State != "resolved" || i.Resolution != "confirmed" {
			t.Errorf("incident = %+v", i)
		}
		es := in.latest(t, 2)
		if es[0].Action != "key.revoke" || es[0].IncidentID != 1 || es[1].Action != "incident.resolve" || es[1].IncidentID != 1 {
			t.Errorf("audit = %+v", es)
		}
	})
	t.Run("compromise", func(t *testing.T) {
		in, app, _ := withIncident(t)
		if _, err := in.svc.CompromiseCredential(as("owner"), admin.CompromiseCredentialRequest{Ref: "main", Secret: replacement, IncidentID: 1}); err != nil {
			t.Fatal(err)
		}
		if i := in.incident(t, 1); i.State != "resolved" || i.Resolution != "confirmed" {
			t.Errorf("incident = %+v", i)
		}
		// The compromise entry and the rotation of the incident's own key carry
		// the id; the other key's rotation does not.
		var withID, without []string
		for _, e := range in.latest(t, 6) {
			if e.Action == "credential.compromise" || e.Action == "key.rotate" || e.Action == "incident.resolve" {
				if e.IncidentID == 1 {
					withID = append(withID, e.Action+"/"+e.TargetID)
				} else {
					without = append(without, e.Action+"/"+e.TargetID)
				}
			}
		}
		if len(withID) != 3 || len(without) != 1 || !strings.Contains(strings.Join(withID, " "), "key.rotate/"+app.Key.ID) {
			t.Errorf("with incident %v, without %v", withID, without)
		}
	})
}

func TestTheIncidentOfAContainmentCommandMustExistBelongToTheKeyAndBeUnresolved(t *testing.T) {
	in, app, batch := withIncident(t)
	// Another key's incident.
	if _, err := in.svc.DisableKey(as("owner"), admin.ContainKeyRequest{ID: batch.Key.ID, IncidentID: 1}); code(err) != admin.CodeConflict {
		t.Errorf("another key's incident = %v, want conflict", err)
	}
	if _, err := in.svc.DisableKey(as("owner"), admin.ContainKeyRequest{ID: app.Key.ID, IncidentID: 99}); code(err) != admin.CodeNotFound {
		t.Errorf("an unknown incident = %v, want not found", err)
	}
	// None of the refusals changed the key.
	for _, k := range []string{app.Key.ID, batch.Key.ID} {
		if got, _ := in.svc.GetKey(as("owner"), admin.KeyRef{ID: k}); got.Status != "active" {
			t.Errorf("key %s is %s after a refused command", k, got.Status)
		}
	}
	if _, err := in.svc.ResolveIncident(as("owner"), admin.ResolveIncidentRequest{ID: 1}); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() error{
		"disable": func() error {
			_, err := in.svc.DisableKey(as("owner"), admin.ContainKeyRequest{ID: app.Key.ID, IncidentID: 1})
			return err
		},
		"revoke": func() error {
			_, err := in.svc.RevokeKey(as("owner"), admin.ContainKeyRequest{ID: app.Key.ID, IncidentID: 1})
			return err
		},
		"rotate": func() error {
			_, err := in.svc.RotateKey(as("owner"), admin.RotateKeyRequest{ID: app.Key.ID, IncidentID: 1})
			return err
		},
		"compromise": func() error {
			_, err := in.svc.CompromiseCredential(as("owner"), admin.CompromiseCredentialRequest{Ref: "main", Secret: replacement, IncidentID: 1})
			return err
		},
	} {
		if err := run(); code(err) != admin.CodeConflict {
			t.Errorf("%s with a resolved incident = %v, want conflict", name, err)
		}
	}
	if got, _ := in.svc.GetKey(as("owner"), admin.KeyRef{ID: app.Key.ID}); got.Status != "active" {
		t.Errorf("the key is %s after refused commands", got.Status)
	}
}

func TestCompromiseNeedsTheIncidentsKeyToBeOnTheCredential(t *testing.T) {
	in, app, _ := withIncident(t)
	in.addCredential(t, "spare", "openai")
	if _, err := in.svc.CompromiseCredential(as("owner"), admin.CompromiseCredentialRequest{Ref: "spare", Secret: replacement, IncidentID: 1}); code(err) != admin.CodeConflict {
		t.Errorf("an incident of a key on another credential = %v, want conflict", err)
	}
	if i := in.incident(t, 1); i.State == "resolved" || i.KeyID != app.Key.ID {
		t.Errorf("incident = %+v", i)
	}
}

func TestContainmentWithoutAnIncidentIsAsBefore(t *testing.T) {
	in, app, _ := withIncident(t)
	if _, err := in.svc.RotateKey(as("owner"), admin.RotateKeyRequest{ID: app.Key.ID}); err != nil {
		t.Fatal(err)
	}
	if i := in.incident(t, 1); i.State != "open" {
		t.Errorf("a rotation with no --incident changed the incident: %+v", i)
	}
	for _, e := range in.latest(t, 1) {
		if e.IncidentID != 0 {
			t.Errorf("audit entry %+v carries an incident id", e)
		}
	}
}

func TestRevokingARevokedKeyWithAnIncidentStillResolvesIt(t *testing.T) {
	in, app, _ := withIncident(t)
	if _, err := in.svc.RevokeKey(as("owner"), admin.ContainKeyRequest{ID: app.Key.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RevokeKey(as("owner"), admin.ContainKeyRequest{ID: app.Key.ID, IncidentID: 1}); err != nil {
		t.Fatal(err)
	}
	if i := in.incident(t, 1); i.Resolution != "confirmed" {
		t.Errorf("incident = %+v", i)
	}
}

func TestAnIncidentOnACanaryResolvesWhenTheCanaryIsRevoked(t *testing.T) {
	in, _ := withCredential(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"})
	in.detect(t, c.Canary.ID, "canary", store.SeverityCritical, store.Evidence{})
	if _, err := in.svc.RevokeKey(as("owner"), admin.ContainKeyRequest{ID: c.Canary.ID, IncidentID: 1}); err != nil {
		t.Fatal(err)
	}
	if i := in.incident(t, 1); i.Resolution != "confirmed" || i.Severity != "critical" {
		t.Errorf("incident = %+v", i)
	}
}

// #89: a new network's detection lists the networks the key normally uses, which
// are addresses, so they are withheld with the rest; what is usual in names that
// are not addresses (countries, ASNs, clients) is always given.
func TestTheUsualNetworksOfANewNetworkDetectionAreWithheldLikeAddresses(t *testing.T) {
	in, app, _ := withIncident(t)
	in.detect(t, app.Key.ID, "new_network", store.SeverityLow, store.Evidence{Address: "198.51.100.9", Network: "198.51.100.0/24",
		Observed: "new network", Baseline: "2 usual networks", Usual: "203.0.113.0/24 (40 requests), 192.0.2.0/24 (2 requests)"})
	d, err := in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := d.Detections[1].Evidence
	if got.Usual != "" || !got.AddressWithheld || got.Baseline != "2 usual networks" || got.Observed != "new network" {
		t.Errorf("evidence = %+v, want the usual networks withheld and the counts and name given", got)
	}
	in.exposeClients(t, true)
	if d, err = in.svc.GetIncident(as("owner"), admin.IncidentRef{ID: 1}); err != nil || d.Detections[1].Evidence.Usual == "" {
		t.Errorf("with the setting on, usual = %q (%v), want the networks", d.Detections[1].Evidence.Usual, err)
	}
}
