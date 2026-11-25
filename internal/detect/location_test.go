package detect_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
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

// A key used only from private addresses has a network and no country or ASN:
// the first it is seen with after learning is a thief abroad, not a baseline.
func TestAFirstPlaceOfAKindAfterLearningIsRaisedAndDuringLearningIsNot(t *testing.T) {
	r := newRig(t)
	noGeo := func(e *events.Event) { e.Country, e.ASN, e.ASOrg = "", 0, "" }
	r.mustWrite(ev(t0, noGeo))
	// During learning the first country and ASN are only recorded.
	r.mustWrite(ev(t0.Add(24*time.Hour), nil))
	if got := r.alerts.got(); len(got) != 0 {
		t.Fatalf("a first country and ASN during learning raised %+v", got)
	}
	// A key that has had no country or ASN when its learning ends: the first it
	// ever shows is new.
	r.mustWrite(ev(t0.Add(time.Hour), func(e *events.Event) { e.KeyID = "beef5678"; noGeo(e) }))
	r.mustWrite(ev(afterLearning.Add(time.Hour), func(e *events.Event) { e.KeyID = "beef5678" }))
	got := r.alerts.got()
	if len(got) != 1 || got[0].det.Rule != "new_country" || got[0].inc.KeyID != "beef5678" || len(got[0].inc.Rules()) != 2 {
		t.Fatalf("alerts = %+v, want new_country (and new_asn on the incident) for the key that learned none", got)
	}
	var ev store.Evidence
	json.Unmarshal([]byte(got[0].det.EvidenceJSON), &ev)
	if ev.Baseline != "no usual places recorded" {
		t.Errorf("baseline = %q, want it to say there were no usual places", ev.Baseline)
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

func TestAKnownCIDRSilencesTheRequestAndAKnownCountryOrASNOnlyItsOwnRule(t *testing.T) {
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
		// a known network: everything about the request is silenced
		ev(afterLearning, func(e *events.Event) {
			e.Country, e.ASN, e.Client, e.ClientIP = "DE", 4000, "wget/1", netip.MustParseAddr("203.0.113.50")
		}),
		// a known ASN, from an unknown country and client: only the ASN is silenced
		ev(afterLearning.Add(time.Minute), func(e *events.Event) { e.Country, e.ASN, e.Client = "FR", 3320, "curl/8" }),
	)
	var rules []string
	for _, a := range r.alerts.got() {
		rules = append(rules, a.det.Rule)
	}
	got := r.alerts.got()
	if len(got) != 1 || got[0].det.Rule != "new_country" || strings.Join(got[0].inc.Rules(), ",") != "new_country,new_client" {
		t.Fatalf("alerts %v, incident rules %v: want the country and client of the second request, and neither the first request nor the known ASN", rules, got[0].inc.Rules())
	}
	var e store.Evidence
	json.Unmarshal([]byte(got[0].det.EvidenceJSON), &e)
	if e.Country != "FR" {
		t.Errorf("the detection is about %q, want FR", e.Country)
	}
}

// A known source covering the first event of a batch must not shadow a thief
// later in the same batch.
func TestAKnownSourceMatchForOneRequestOfABatchDoesNotShadowAnother(t *testing.T) {
	r := learned(t)
	if err := r.store.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.CreateKnownSource(store.KnownSource{Kind: store.SourceCIDR, Value: "203.0.113.0/24", Label: "ours", CreatedBy: "owner", CreatedAt: t0})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r.mustWrite(
		ev(afterLearning, func(e *events.Event) { e.Country, e.ClientIP = "DE", netip.MustParseAddr("203.0.113.50") }),
		ev(afterLearning.Add(time.Second), func(e *events.Event) { e.Country, e.ClientIP = "FR", netip.MustParseAddr("192.0.2.9") }),
	)
	got := r.alerts.got()
	if len(got) != 1 {
		t.Fatalf("alerts = %+v, want one, for the request outside the known network", got)
	}
	var e store.Evidence
	json.Unmarshal([]byte(got[0].det.EvidenceJSON), &e)
	if e.Country != "FR" {
		t.Errorf("the detection is about %q, want FR", e.Country)
	}
}

// With no learning period the key is watched from its first forwarded request,
// and that request is the baseline, whatever the clock's precision: the store
// keeps times in milliseconds and events have nanoseconds.
func TestWithNoLearningPeriodTheFirstForwardedRequestIsStillTheBaseline(t *testing.T) {
	r := newRig(t)
	if err := r.store.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.PutSetting(store.Setting{Key: store.SettingLearningPeriod, ValueJSON: "0", UpdatedAt: t0})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	first := t0.Add(123456789 * time.Nanosecond)
	r.mustWrite(ev(first, nil))
	if got := r.alerts.got(); len(got) != 0 {
		t.Fatalf("the first request of a key raised %+v", got)
	}
	r.mustWrite(ev(first.Add(time.Second), func(e *events.Event) { e.Country = "DE" }))
	if got := r.alerts.got(); len(got) != 1 || got[0].det.Rule != "new_country" {
		t.Errorf("alerts = %+v, want new_country for the next request", got)
	}
}

// A request that sprays new networks at a key (a thief behind rotating proxies,
// refused or not) must not make the writer's transaction slow: it holds the write
// lock that every event waits on.
func TestAThousandNewPlacesInOneBatchAreRaisedInBoundedTime(t *testing.T) {
	r := learned(t)
	var batch []events.Event
	for i := range 1000 {
		batch = append(batch, ev(afterLearning.Add(time.Duration(i)*time.Millisecond), func(e *events.Event) {
			e.Kind, e.Status, e.BlockedCode = events.KindBlocked, 403, "key_disabled"
			e.ClientIP = netip.AddrFrom4([4]byte{10, byte(i / 256), byte(i % 256), 1})
			e.ASN, e.ASOrg, e.Client = int64(100000+i), "Proxy "+strconv.Itoa(i), "bot-"+strconv.Itoa(i)+"/1"
		}))
	}
	began := time.Now()
	r.mustWrite(batch...)
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("1000 requests, each new in network, ASN and client, took %v in the writer's transaction", took)
	}
	ds := r.detections(1)
	if len(ds) > detect.MaxDetectionsPerIncident+8 {
		t.Errorf("%d detection rows for one batch, want at most the bound", len(ds))
	}
	inc := r.incident(1)
	if got := strings.Join(inc.Rules(), ","); got != "new_asn,new_client,new_network" {
		t.Errorf("rules = %s, want every rule that fired, however many requests there were", got)
	}
	// A key keeps 1,000 places of a kind, its usual one among them, so 999 of
	// each kind are new and the last request's are only counted as places.
	if inc.Detections != 3*999 {
		t.Errorf("the incident counted %d detections, want every one of the 2,997 new places", inc.Detections)
	}
	if n := len(r.alerts.got()); n != 1 {
		t.Errorf("%d alerts for one batch, want 1", n)
	}
}

// Many keys spraying new places at once must not make the writer's transaction
// slow either: past the batch's bound a key raises only the rules it has not
// raised yet in the batch, so each incident still opens with every rule, and
// what is not raised is still counted on it (#113).
func TestManyKeysSprayingNewPlacesInOneBatchAreBoundedTogetherAndStillCounted(t *testing.T) {
	r := newRig(t)
	for _, k := range []string{"abcd1234", "beef5678"} {
		r.mustWrite(ev(t0, func(e *events.Event) { e.KeyID = k }))
		r.mustWrite(ev(t0.Add(time.Hour), func(e *events.Event) { e.KeyID = k }))
	}
	r.engine = detect.New(detect.Options{Store: r.store, Now: func() time.Time { return r.now }, Alerts: r.alerts, MaxDetectionGroupsPerBatch: 5})
	var batch []events.Event
	for i := range 40 {
		key := "abcd1234"
		if i >= 20 {
			key = "beef5678"
		}
		batch = append(batch, ev(afterLearning.Add(time.Duration(i)*time.Millisecond), func(e *events.Event) {
			e.KeyID = key
			e.ClientIP = netip.AddrFrom4([4]byte{10, 1, byte(i), 1})
			e.ASN, e.Client = int64(100000+i), "bot-"+strconv.Itoa(i)+"/1"
		}))
	}
	r.mustWrite(batch...)
	// abcd1234 used up the batch's five groups; beef5678 raised one, for its rules.
	for id, want := range map[int64]int{1: 5 * 3, 2: 3} {
		inc := r.incident(id)
		if n := len(r.detections(id)); n != want {
			t.Errorf("incident %d (%s): %d detection rows, want %d", id, inc.KeyID, n, want)
		}
		if got := strings.Join(inc.Rules(), ","); got != "new_asn,new_client,new_network" {
			t.Errorf("incident %d rules = %s", id, got)
		}
		if inc.Detections != 20*3 {
			t.Errorf("incident %d counted %d detections, want all 60 new places", id, inc.Detections)
		}
		if want := afterLearning.Add(time.Duration(19+20*(id-1)) * time.Millisecond); !inc.LastFiredAt.Equal(want) {
			t.Errorf("incident %d last fired %v, want its last request's %v", id, inc.LastFiredAt, want)
		}
	}
}

// sprayer is a request of key from a place new in network, ASN and client.
func sprayer(key string, at time.Time, n int) events.Event {
	return ev(at, func(e *events.Event) {
		e.KeyID = key
		e.ClientIP = netip.AddrFrom4([4]byte{10, byte(n / 256), byte(n % 256), 1})
		e.ASN, e.Client = int64(100000+n), "bot-"+strconv.Itoa(n%1000)+"/1"
	})
}

// boundedRig is a learned rig for both standard keys whose engine raises at
// most budget detection groups a batch.
func boundedRig(t *testing.T, budget int) *rig {
	r := newRig(t)
	for _, k := range []string{"abcd1234", "beef5678"} {
		r.mustWrite(ev(t0, func(e *events.Event) { e.KeyID = k }))
		r.mustWrite(ev(t0.Add(time.Hour), func(e *events.Event) { e.KeyID = k }))
	}
	r.engine = detect.New(detect.Options{Store: r.store, Now: func() time.Time { return r.now }, Alerts: r.alerts, MaxDetectionGroupsPerBatch: budget})
	return r
}

// Counting what the bound held back must not stop an incident recording its
// first 100 detections: a key held back in one batch still records who and
// where in the next (SPEC §10).
func TestAKeyHeldBackByTheBatchBoundStillRecordsItsLaterDetections(t *testing.T) {
	r := boundedRig(t, 5)
	var batch []events.Event
	for i := range 80 {
		key := "abcd1234"
		if i >= 20 {
			key = "beef5678"
		}
		batch = append(batch, sprayer(key, afterLearning.Add(time.Duration(i)*time.Millisecond), i))
	}
	r.mustWrite(batch...)
	if inc := r.incident(2); len(r.detections(2)) != 3 || inc.Detections != 60*3 {
		t.Fatalf("incident 2: %d rows, counter %d; want 3 and 180", len(r.detections(2)), inc.Detections)
	}
	for i := range 3 {
		r.mustWrite(sprayer("beef5678", afterLearning.Add(time.Duration(i+1)*time.Minute), 500+i))
	}
	if n := len(r.detections(2)); n != 3+3*3 {
		t.Errorf("incident 2 has %d rows after three more new places, want 12: it is far from 100 rows", n)
	}
}

// Past the bound a request from a known source is not counted on the incident
// and does not move its last firing: the operator's own traffic must not keep an
// incident from going quiet.
func TestPastTheBoundAKnownSourceIsNotCounted(t *testing.T) {
	r := boundedRig(t, 1)
	if err := r.store.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.CreateKnownSource(store.KnownSource{Scope: "abcd1234", Kind: store.SourceCIDR, Value: "10.9.0.0/16", Label: "ours", CreatedBy: "owner", CreatedAt: t0})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var batch []events.Event
	for i := range 5 {
		batch = append(batch, sprayer("abcd1234", afterLearning.Add(time.Duration(i)*time.Millisecond), i))
	}
	for i := range 5 {
		batch = append(batch, sprayer("abcd1234", afterLearning.Add(time.Duration(10+i)*time.Millisecond), 9*256+i))
	}
	r.mustWrite(batch...)
	inc := r.incident(1)
	if inc.Detections != 5*3 || !inc.LastFiredAt.Equal(afterLearning.Add(4*time.Millisecond)) {
		t.Errorf("counter %d, last fired %v: want 15 and the last unknown request's time", inc.Detections, inc.LastFiredAt)
	}
}

// A batch the writer catches up with can span an hour. Past the bound a place
// that fires on an incident gone quiet is still raised, since that escalates.
func TestPastTheBoundAFiringOnAQuietIncidentIsStillRaised(t *testing.T) {
	r := boundedRig(t, 1)
	r.mustWrite(
		sprayer("abcd1234", afterLearning, 1),
		sprayer("abcd1234", afterLearning.Add(time.Second), 2),
		sprayer("abcd1234", afterLearning.Add(2*time.Hour), 3),
	)
	if n := len(r.detections(1)); n != 6 {
		t.Errorf("%d rows, want 3 for the first request and 3 for the one after two quiet hours", n)
	}
	if n := len(r.alerts.got()); n != 2 {
		t.Errorf("%d alerts, want the open and the escalation", n)
	}
}

// A place that was only counted is still a firing: a later place is measured
// from it, so a batch that is quiet from the last raise but not from the last
// counted place does not escalate.
func TestPastTheBoundACountedPlaceIsTheLastFiringForTheQuietCheck(t *testing.T) {
	r := boundedRig(t, 1)
	r.mustWrite(
		sprayer("abcd1234", afterLearning, 1),
		sprayer("abcd1234", afterLearning.Add(50*time.Minute), 2),
		sprayer("abcd1234", afterLearning.Add(100*time.Minute), 3),
	)
	if n := len(r.alerts.got()); n != 1 {
		t.Errorf("%d alerts, want only the open: no hour went by without a firing", n)
	}
	if n := len(r.detections(1)); n != 3 {
		t.Errorf("%d rows, want the first request's 3", n)
	}
}

// The worst case with many keys: each still opens its incident, so the cost
// grows with the keys, but not with their requests (ADR 0015).
func TestAHundredKeysSprayingNewPlacesInOneBatchAreRaisedInBoundedTime(t *testing.T) {
	keys := []store.VirtualKey{}
	for i := range 100 {
		keys = append(keys, store.VirtualKey{ID: fmt.Sprintf("k%07d", i), Name: fmt.Sprintf("app-%d", i), CredentialID: "cr_1"})
	}
	st := seedStore(t, keys...)
	r := &rig{t: t, store: st, alerts: &alerter{}, now: t0}
	r.engine = detect.New(detect.Options{Store: st, Now: func() time.Time { return r.now }, Alerts: r.alerts})
	var learn, later, batch []events.Event
	for _, k := range keys {
		learn = append(learn, ev(t0, func(e *events.Event) { e.KeyID = k.ID }))
		later = append(later, ev(t0.Add(time.Hour), func(e *events.Event) { e.KeyID = k.ID }))
	}
	r.mustWrite(learn...)
	r.mustWrite(later...)
	for i := range 100 * 50 {
		batch = append(batch, sprayer(keys[i%100].ID, afterLearning.Add(time.Duration(i)*time.Millisecond), i))
	}
	began := time.Now()
	r.mustWrite(batch...)
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("100 keys of 50 requests each, every one new in network, ASN and client, took %v in the writer's transaction", took)
	}
	rows := 0
	for id := range int64(100) {
		inc := r.incident(id + 1)
		// 50 networks and ASNs each, and 10 clients: sprayer's clients repeat
		// every 1,000 requests, every 10th of a key's.
		if inc.Detections != 50+50+10 || len(inc.Rules()) != 3 {
			t.Errorf("incident %d: counter %d, rules %v; want 110 and all three", id+1, inc.Detections, inc.Rules())
		}
		rows += len(r.detections(id + 1))
	}
	if bound := 3 * (detect.MaxDetectionGroupsPerBatch + 3*100); rows > bound {
		t.Errorf("%d detection rows, want at most %d", rows, bound)
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
