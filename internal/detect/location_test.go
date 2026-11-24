package detect_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/detect"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

// A request from here, on key abcd1234 unless the event says otherwise.
func ev(at time.Time, mut func(*events.Event)) events.Event {
	e := events.Event{Time: at, Kind: events.KindRequest, KeyID: "abcd1234", ClientIP: netip.MustParseAddr("198.51.100.7"),
		Country: "US", ASN: 64500, ASOrg: "Example Net", Client: "openai-python/3", Status: 200}
	if mut != nil {
		mut(&e)
	}
	return e
}

func (r *rig) write(events_ ...events.Event) error {
	r.t.Helper()
	if len(events_) > 0 {
		r.now = events_[len(events_)-1].Time
	}
	return events.SQLite(r.store, r.engine).AddEvents(context.Background(), events_)
}

func (r *rig) mustWrite(events_ ...events.Event) {
	r.t.Helper()
	if err := r.write(events_...); err != nil {
		r.t.Fatal(err)
	}
}

// learned is a rig whose key abcd1234 has been used for longer than the learning
// period from the usual place, and is now watched.
func learned(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.mustWrite(ev(t0, nil))
	r.mustWrite(ev(t0.Add(time.Hour), nil))
	return r
}

var afterLearning = t0.Add(7*24*time.Hour + time.Minute)

func TestADetectionIsRaisedInTheTransactionThatRecordsThePlace(t *testing.T) {
	r := learned(t)
	r.alerts.fail = errors.New("the outbox is full")
	err := r.write(ev(afterLearning, func(e *events.Event) { e.Country = "DE" }))
	if err == nil {
		t.Fatal("the batch was stored although its detection could not be")
	}
	// Nothing of the batch survived: the place is not recorded, so it is new again
	// when the writer retries, and the detection is not lost.
	var places []store.PlaceRow
	if err := r.store.Do(context.Background(), func(tx *store.Tx) (err error) { places, err = tx.Places("abcd1234", "country"); return }); err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].Value != "US" {
		t.Fatalf("places = %+v, want only US: the failed batch must leave no place behind", places)
	}
	r.alerts.fail = nil
	r.mustWrite(ev(afterLearning, func(e *events.Event) { e.Country = "DE" }))
	if got := r.alerts.got(); len(got) != 1 || got[0].det.Rule != "new_country" {
		t.Fatalf("alerts = %+v, want the retry to raise new_country exactly once", got)
	}
	// And once only: the place is known now.
	r.mustWrite(ev(afterLearning.Add(time.Minute), func(e *events.Event) { e.Country = "DE" }))
	if got := r.alerts.got(); len(got) != 1 {
		t.Errorf("%d alerts after the place was seen again, want 1", len(got))
	}
	if r.alerts.wakes != 1 {
		t.Errorf("woke the dispatcher %d times, want once, after the commit that queued the alert", r.alerts.wakes)
	}
}

func TestEachRuleHasItsSeverityAndTheEvidenceNamesThePlaceAndWhatIsUsual(t *testing.T) {
	for _, c := range []struct {
		name     string
		mut      func(*events.Event)
		rule     string
		severity store.Severity
		observed string
		baseline string
	}{
		{"country", func(e *events.Event) { e.Country = "DE" }, "new_country", store.SeverityHigh, "new country DE", "US (2 requests)"},
		{"asn", func(e *events.Event) { e.ASN, e.ASOrg = 3320, "Deutsche Telekom AG" }, "new_asn", store.SeverityMedium, "new ASN AS3320 (Deutsche Telekom AG)", "AS64500 Example Net (2 requests)"},
		{"network", func(e *events.Event) { e.ClientIP = netip.MustParseAddr("203.0.113.9") }, "new_network", store.SeverityLow, "new network", "1 usual network"},
		{"client", func(e *events.Event) { e.Client = "curl/8" }, "new_client", store.SeverityMedium, "new client curl/8", "openai-python/3 (2 requests)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := learned(t)
			r.mustWrite(ev(afterLearning, c.mut))
			a := r.alerts.got()
			if len(a) != 1 || a[0].det.Rule != c.rule || a[0].det.Severity != c.severity || a[0].change != "opened" {
				t.Fatalf("alerts = %+v, want one opening for %s at %s", a, c.rule, c.severity)
			}
			var got store.Evidence
			if err := json.Unmarshal([]byte(a[0].det.EvidenceJSON), &got); err != nil {
				t.Fatal(err)
			}
			if got.Observed != c.observed || got.Baseline != c.baseline {
				t.Errorf("evidence observed %q baseline %q, want %q and %q", got.Observed, got.Baseline, c.observed, c.baseline)
			}
			if c.name == "network" {
				if got.Usual != "198.51.100.0/24 (2 requests)" || got.Network != "203.0.113.0/24" {
					t.Errorf("a network detection's usual = %q, network = %q", got.Usual, got.Network)
				}
			} else if got.Usual != "" {
				t.Errorf("usual = %q: only a new network lists networks, which are addresses", got.Usual)
			}
			if got.Address != "198.51.100.7" && c.name != "network" {
				t.Errorf("address = %q, want where the request came from", got.Address)
			}
		})
	}
}

func TestOnlyTheFewestBusiestUsualPlacesAreNamed(t *testing.T) {
	r := newRig(t)
	for i, c := range []string{"US", "US", "US", "US", "FR", "FR", "FR", "DE", "DE", "NL", "BE"} {
		r.mustWrite(ev(t0.Add(time.Duration(i)*time.Minute), func(e *events.Event) { e.Country = c }))
	}
	r.mustWrite(ev(afterLearning, func(e *events.Event) { e.Country = "CN" }))
	var got store.Evidence
	a := r.alerts.got()
	if len(a) != 1 {
		t.Fatalf("alerts = %+v", a)
	}
	json.Unmarshal([]byte(a[0].det.EvidenceJSON), &got)
	if want := "US (4 requests), FR (3 requests), DE (2 requests), and 2 more"; got.Baseline != want {
		t.Errorf("baseline = %q, want %q", got.Baseline, want)
	}
}

func TestARequestNewFromEverywhereIsOneAlertThatNamesTheHighestRule(t *testing.T) {
	r := learned(t)
	r.mustWrite(ev(afterLearning, func(e *events.Event) {
		e.Country, e.ASN, e.ASOrg, e.ClientIP, e.Client = "DE", 3320, "Deutsche Telekom AG", netip.MustParseAddr("203.0.113.9"), "curl/8"
	}))
	a := r.alerts.got()
	if len(a) != 1 {
		t.Fatalf("%d alerts for one request, want 1: %+v", len(a), a)
	}
	if a[0].det.Rule != "new_country" || a[0].inc.Severity != store.SeverityHigh {
		t.Errorf("the alert is about %s at %s, want the highest, new_country at high", a[0].det.Rule, a[0].inc.Severity)
	}
	if got := strings.Join(a[0].inc.Rules(), ","); got != "new_country,new_asn,new_client,new_network" {
		t.Errorf("the incident has rules %s, want all four, highest severity first", got)
	}
	if a[0].inc.Detections != 4 || r.alerts.wakes != 1 {
		t.Errorf("incident = %+v, wakes %d, want 4 detections and one wake", a[0].inc, r.alerts.wakes)
	}
	// A later new client is a rule the incident has had: a repeat, and silent.
	r.mustWrite(ev(afterLearning.Add(time.Minute), func(e *events.Event) { e.Client = "wget/1" }))
	if a = r.alerts.got(); len(a) != 1 {
		t.Errorf("a second new client (a rule the incident has) alerted: %d alerts", len(a))
	}
}

func TestAnEscalationInAGroupStillAlertsWhenTheHighestRuleIsOnlyARepeat(t *testing.T) {
	r := learned(t)
	r.mustWrite(ev(afterLearning, func(e *events.Event) { e.Country = "DE" })) // opens: new_country
	r.mustWrite(ev(afterLearning.Add(time.Minute), func(e *events.Event) { e.Country, e.ASN = "FR", 3215 }))
	a := r.alerts.got()
	if len(a) != 2 || a[1].change != "escalated" || a[1].det.Rule != "new_asn" {
		t.Fatalf("alerts = %+v, want an escalation for new_asn: the country is a repeat and the ASN is a rule the incident has not had", a)
	}
}

func TestAFirstPlaceOfAKindIsNotNewBecauseThereIsNothingToCompareWithIt(t *testing.T) {
	r := newRig(t)
	// Used for weeks with no GeoIP database, then one is installed.
	r.mustWrite(ev(t0, func(e *events.Event) { e.Country, e.ASN, e.ASOrg = "", 0, "" }))
	r.mustWrite(ev(afterLearning, nil))
	if got := r.alerts.got(); len(got) != 0 {
		t.Errorf("the first country and ASN a key is ever seen with raised %+v", got)
	}
	// Now it has a baseline of those kinds, and a different country is new.
	r.mustWrite(ev(afterLearning.Add(time.Hour), func(e *events.Event) { e.Country = "DE" }))
	if got := r.alerts.got(); len(got) != 1 || got[0].det.Rule != "new_country" {
		t.Errorf("alerts = %+v, want new_country", got)
	}
}

func TestNothingIsRaisedForAKeyThatWasNeverForwardedARequest(t *testing.T) {
	r := newRig(t)
	refused := func(at time.Time, country string) events.Event {
		return ev(at, func(e *events.Event) {
			e.Kind, e.Status, e.BlockedCode, e.Country = events.KindBlocked, 403, "address_not_allowed", country
		})
	}
	r.mustWrite(refused(t0, "US"))
	r.mustWrite(refused(afterLearning, "DE"))
	if got := r.alerts.got(); len(got) != 0 {
		t.Fatalf("a key with no forwarded request has no baseline and raised %+v", got)
	}
	// Its first forwarded request starts the learning period, which the rules wait out.
	r.mustWrite(ev(afterLearning.Add(time.Hour), func(e *events.Event) { e.Country = "FR" }))
	r.mustWrite(ev(afterLearning.Add(2*time.Hour), func(e *events.Event) { e.Country = "NL" }))
	if got := r.alerts.got(); len(got) != 0 {
		t.Fatalf("the learning period had only just started and raised %+v", got)
	}
	r.mustWrite(ev(afterLearning.Add(time.Hour+7*24*time.Hour), func(e *events.Event) { e.Country = "SE" }))
	if got := r.alerts.got(); len(got) != 1 || got[0].det.Rule != "new_country" {
		t.Errorf("alerts = %+v, want new_country once the period was over", got)
	}
}

func TestThePolicyAndTheSettingSetTheLearningPeriodAndAKeyMayTurnARuleOff(t *testing.T) {
	r := newRig(t)
	put := func(id, policy string) {
		t.Helper()
		err := r.store.Do(context.Background(), func(tx *store.Tx) error {
			k, err := tx.Key(id)
			if err != nil {
				return err
			}
			k.PolicyJSON = policy
			_, err = tx.UpdateKey(k)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setting := func(secs string) {
		t.Helper()
		if err := r.store.Do(context.Background(), func(tx *store.Tx) error {
			_, err := tx.PutSetting(store.Setting{Key: store.SettingLearningPeriod, ValueJSON: secs, UpdatedAt: t0})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"abcd1234", "beef5678"} {
		r.mustWrite(ev(t0, func(e *events.Event) { e.KeyID = id }))
	}
	// The install learns for 2 days; abcd1234 for 5 minutes by its policy; beef5678 has rules off.
	setting("172800")
	put("abcd1234", `{"learning_period":"5m"}`)
	put("beef5678", `{"detect":{"new_country":false}}`)
	at := t0.Add(time.Hour)
	r.mustWrite(
		ev(at, func(e *events.Event) { e.Country = "DE" }),                                       // abcd1234: past its own 5 minutes
		ev(at, func(e *events.Event) { e.KeyID, e.Country, e.Client = "beef5678", "DE", "x/1" }), // beef5678: still learning, and country is off
	)
	got := r.alerts.got()
	if len(got) != 1 || got[0].inc.KeyID != "abcd1234" {
		t.Fatalf("alerts = %+v, want only abcd1234 (the install's 2 days have not passed for beef5678)", got)
	}
	// After the install's period beef5678 is watched, except for the rule it turned off.
	at = t0.Add(49 * time.Hour)
	r.mustWrite(ev(at, func(e *events.Event) { e.KeyID, e.Country, e.Client = "beef5678", "FR", "y/1" }))
	got = r.alerts.got()
	if len(got) != 2 || got[1].det.Rule != "new_client" {
		t.Fatalf("alerts = %+v, want new_client for beef5678 (new_country is turned off)", got)
	}
	// A learning period of 0 is none: watched from the first forwarded request on.
	put("beef5678", `{"learning_period":"0s"}`)
	r.mustWrite(ev(at.Add(10*time.Minute), func(e *events.Event) { e.KeyID, e.Client = "beef5678", "z/1" }))
	if got = r.alerts.got(); len(got) != 2 { // the same incident, the same rule: a repeat
		t.Errorf("alerts = %+v, want a repeat to stay silent", got)
	}
}

func TestAPolicyThatCannotBeReadMeansTheDefaultsAndDoesNotStopEvents(t *testing.T) {
	r := learned(t)
	if err := r.store.Do(context.Background(), func(tx *store.Tx) error {
		k, _ := tx.Key("abcd1234")
		k.PolicyJSON = `{not json`
		_, err := tx.UpdateKey(k)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r.mustWrite(ev(afterLearning, func(e *events.Event) { e.Country = "DE" }))
	if got := r.alerts.got(); len(got) != 1 {
		t.Errorf("alerts = %+v, want the key watched with the defaults", got)
	}
}

func TestACanaryAndAnUnknownKeyAreLeftToTheirOwnRules(t *testing.T) {
	r := newRig(t)
	for _, id := range []string{"cafe0001", "nokey000"} {
		r.mustWrite(ev(t0, func(e *events.Event) { e.KeyID = id }))
		r.mustWrite(ev(afterLearning, func(e *events.Event) { e.KeyID, e.Country = id, "DE" }))
	}
	if got := r.alerts.got(); len(got) != 0 {
		t.Errorf("alerts = %+v, want none: a canary's use is its own critical rule, and an unknown key has no incident to join", got)
	}
}

func TestKnownSourcesSilenceTheRulesForTheRequestsFromThem(t *testing.T) {
	r := learned(t)
	add := func(scope, kind, value string) {
		t.Helper()
		if err := r.store.Do(context.Background(), func(tx *store.Tx) error {
			_, err := tx.CreateKnownSource(store.KnownSource{Scope: scope, Kind: kind, Value: value, Label: "ours", CreatedBy: "owner", CreatedAt: t0})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("abcd1234", store.SourceCIDR, "203.0.113.0/24")
	add("", store.SourceASN, "3320")
	add("beef5678", store.SourceCountry, "FR") // another key's: does not apply
	r.mustWrite(
		ev(afterLearning, func(e *events.Event) { e.Country, e.ClientIP = "DE", netip.MustParseAddr("203.0.113.50") }), // a known network
		ev(afterLearning.Add(time.Minute), func(e *events.Event) { e.Country, e.ASN = "DE", 3320 }),                    // a known ASN, new country
		ev(afterLearning.Add(2*time.Minute), func(e *events.Event) { e.Country, e.Client = "FR", "curl/8" }),           // not covered for this key
	)
	got := r.alerts.got()
	if len(got) != 1 || got[0].det.Rule != "new_country" {
		t.Fatalf("alerts = %+v, want only the request nothing covers", got)
	}
	var ev store.Evidence
	json.Unmarshal([]byte(got[0].det.EvidenceJSON), &ev)
	if ev.Country != "FR" {
		t.Errorf("the detection is about %q, want FR", ev.Country)
	}
}

func TestTheTextOfEvidenceIsCleanedOfControlCharacters(t *testing.T) {
	r := learned(t)
	r.mustWrite(ev(afterLearning, func(e *events.Event) { e.ASN, e.ASOrg = 3320, "Evil\x1b[31m\nOrg\r\n"+strings.Repeat("x", 200) }))
	a := r.alerts.got()
	if len(a) != 1 {
		t.Fatalf("alerts = %+v", a)
	}
	var got store.Evidence
	json.Unmarshal([]byte(a[0].det.EvidenceJSON), &got)
	for _, s := range []string{got.ASOrg, got.Observed, got.Baseline, got.Detail} {
		if strings.ContainsAny(s, "\x1b\r\n\t") || len(s) > 400 {
			t.Errorf("evidence text %q has a control character or is not bounded", s)
		}
	}
	if !strings.HasPrefix(got.ASOrg, "Evil [31m Org xxx") && !strings.HasPrefix(got.ASOrg, "Evil") {
		t.Errorf("as_org = %q", got.ASOrg)
	}
}

func TestARefusedRequestFromANewPlaceIsRaisedAndSaysItWasRefused(t *testing.T) {
	r := learned(t)
	r.mustWrite(ev(afterLearning, func(e *events.Event) {
		e.Kind, e.Status, e.BlockedCode, e.Country = events.KindBlocked, 403, "address_not_allowed", "DE"
	}))
	a := r.alerts.got()
	if len(a) != 1 || a[0].det.Rule != "new_country" {
		t.Fatalf("alerts = %+v, want new_country", a)
	}
	var got store.Evidence
	json.Unmarshal([]byte(a[0].det.EvidenceJSON), &got)
	if !strings.Contains(got.Detail, "refused") || !strings.Contains(got.Detail, "address_not_allowed") {
		t.Errorf("detail = %q, want it to say the request was refused and why", got.Detail)
	}
}

var _ = detect.Opened
