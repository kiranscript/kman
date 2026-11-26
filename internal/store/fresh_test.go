package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/store"
)

func addFresh(t *testing.T, s *store.Store, rows []store.EventRow) []store.NewPlace {
	t.Helper()
	var fresh []store.NewPlace
	do(t, s, func(tx *store.Tx) (err error) { fresh, err = tx.AddEventsFresh(rows); return })
	return fresh
}

func freshSummary(f []store.NewPlace) []string {
	var out []string
	for _, p := range f {
		out = append(out, fmt.Sprintf("%s/%s/%s event=%d", p.KeyID, p.Kind, p.Value, p.Event))
	}
	return out
}

func TestAddEventsFreshSaysWhichPlacesWereNewToTheirKey(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	first := addFresh(t, s, []store.EventRow{
		{Time: t0, Kind: "request", KeyID: "kkkk1111", Country: "DE", ASN: 3320, ASOrg: "Telekom", Client: "curl/8", Status: 200},
	})
	if len(first) != 3 {
		t.Fatalf("a key's first request gave fresh places %v, want its country, ASN and client", freshSummary(first))
	}

	second := addFresh(t, s, []store.EventRow{
		{Time: t0.Add(time.Hour), Kind: "request", KeyID: "kkkk1111", Country: "DE", ASN: 3320, Client: "curl/8", Status: 200}, // nothing new
		{Time: t0.Add(2 * time.Hour), Kind: "blocked", KeyID: "kkkk1111", Country: "FR", ASN: 3215, ASOrg: "Orange", Client: "curl/8", Status: 403, BlockedCode: "address_not_allowed"},
		{Time: t0.Add(3 * time.Hour), Kind: "request", KeyID: "kkkk1111", Country: "NL", Status: 200},
	})
	want := []string{
		"kkkk1111/country/FR event=1",
		"kkkk1111/asn/3215 event=1",
		"kkkk1111/country/NL event=2",
	}
	got := freshSummary(second)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("fresh = %v\nwant    %v", got, want)
	}
	if second[1].Label != "Orange" || !second[1].At.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("the ASN place = %+v, want its label and when its first event happened", second[1])
	}

	// A place is new exactly once.
	if again := addFresh(t, s, []store.EventRow{{Time: t0.Add(4 * time.Hour), Kind: "request", KeyID: "kkkk1111", Country: "FR", Status: 200}}); len(again) != 0 {
		t.Errorf("a place seen before is fresh again: %v", freshSummary(again))
	}
}

func TestAddEventsFreshNeverCallsTheOverflowRowOrAnEventWithNoKeyNew(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	var batch []store.EventRow
	for i := range store.MaxPlacesPerKind {
		batch = append(batch, store.EventRow{Time: t0.Add(time.Duration(i) * time.Second), Kind: "request", KeyID: "kkkk1111", Client: fmt.Sprintf("app-%d/1", i), Status: 200})
	}
	addFresh(t, s, batch)
	got := addFresh(t, s, []store.EventRow{
		{Time: t0.Add(24 * time.Hour), Kind: "request", KeyID: "kkkk1111", Client: "past-the-limit/1", Status: 200},
		{Time: t0.Add(24 * time.Hour), Kind: "blocked", Client: "curl/8", Country: "DE", Status: 401, BlockedCode: "invalid_key"},
	})
	if len(got) != 0 {
		t.Errorf("fresh = %v, want none: a place counted in the overflow row is not new, and an event with no key has no place", freshSummary(got))
	}
}

func learningStart(t *testing.T, s *store.Store, key string) (time.Time, bool) {
	t.Helper()
	var at time.Time
	var ok bool
	do(t, s, func(tx *store.Tx) (err error) { at, ok, err = tx.LearningStart(key); return })
	return at, ok
}

func TestALearningPeriodStartsAtTheKeysFirstForwardedRequestAndNotAtARefusal(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{{Time: t0, Kind: "blocked", KeyID: "kkkk1111", Country: "DE", Status: 403, BlockedCode: "address_not_allowed"}})
	})
	if at, ok := learningStart(t, s, "kkkk1111"); ok {
		t.Fatalf("a key that was only refused has a learning period from %v", at)
	}
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: t0.Add(2 * time.Hour), Kind: "request", KeyID: "kkkk1111", Status: 200},
			{Time: t0.Add(time.Hour), Kind: "session", KeyID: "kkkk1111", Status: 101},
			{Time: t0.Add(time.Minute), Kind: "blocked", KeyID: "kkkk1111", Status: 403, BlockedCode: "rate_limited"},
		})
	})
	if at, ok := learningStart(t, s, "kkkk1111"); !ok || !at.Equal(t0.Add(time.Hour)) {
		t.Errorf("learning start = %v, %v, want the earliest forwarded request or session at %v", at, ok, t0.Add(time.Hour))
	}
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{{Time: t0.Add(48 * time.Hour), Kind: "request", KeyID: "kkkk1111", Status: 200}})
	})
	if at, _ := learningStart(t, s, "kkkk1111"); !at.Equal(t0.Add(time.Hour)) {
		t.Errorf("a later request moved the learning start to %v", at)
	}
	if _, ok := learningStart(t, s, "kkkk2222"); ok {
		t.Error("a key with no events has a learning period")
	}
}

func TestUsualPlacesAreTheKeysBusiestForwardedOnesWithoutTheNewOnes(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	var batch []store.EventRow
	add := func(n int, country string, kind string) {
		for range n {
			batch = append(batch, store.EventRow{Time: t0, Kind: kind, KeyID: "kkkk1111", Country: country, Status: 200})
			t0 = t0.Add(time.Second)
		}
	}
	add(5, "US", "request")
	add(9, "DE", "request")
	add(2, "FR", "request")
	add(40, "CN", "blocked") // refused only: not a place the key is used from
	add(3, "NL", "request")
	do(t, s, func(tx *store.Tx) error { return tx.AddEvents(batch) })
	var got []store.PlaceRow
	var total int
	do(t, s, func(tx *store.Tx) (err error) {
		got, total, err = tx.UsualPlaces("kkkk1111", store.PlaceCountry, []string{"NL"}, time.Time{}, 3)
		return
	})
	if total != 3 {
		t.Errorf("total usual places = %d, want the 3 forwarded ones outside the exceptions", total)
	}
	var values []string
	for _, p := range got {
		values = append(values, fmt.Sprintf("%s:%d", p.Value, p.Count))
	}
	if fmt.Sprint(values) != "[DE:9 US:5 FR:2]" {
		t.Errorf("usual countries = %v, want [DE:9 US:5 FR:2]", values)
	}
}

func TestUsualPlacesCanBeLimitedToThoseTheKeyHadWhenItFinishedLearning(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	var batch []store.EventRow
	for i, c := range []string{"US", "US", "DE"} { // learned
		batch = append(batch, store.EventRow{Time: t0.Add(time.Duration(i) * time.Hour), Kind: "request", KeyID: "kkkk1111", Country: c, Status: 200})
	}
	for i := range 20 { // a place that turned up after, and has been busy since
		batch = append(batch, store.EventRow{Time: t0.Add(48*time.Hour + time.Duration(i)*time.Minute), Kind: "request", KeyID: "kkkk1111", Country: "CN", Status: 200})
	}
	do(t, s, func(tx *store.Tx) error { return tx.AddEvents(batch) })
	var got []store.PlaceRow
	var total int
	do(t, s, func(tx *store.Tx) (err error) {
		got, total, err = tx.UsualPlaces("kkkk1111", store.PlaceCountry, nil, t0.Add(24*time.Hour), 5)
		return
	})
	if len(got) != 2 || total != 2 || got[0].Value != "US" || got[1].Value != "DE" {
		t.Errorf("usual by the end of learning = %+v (%d), want US then DE and not CN", got, total)
	}
}

// Events are emitted when a request ends, so a batch is not in time order: the
// event that first showed a place is the earliest in time, not the first listed.
func TestAFreshPlaceIsTheEarliestEventOfThePlaceInTheBatch(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	fresh := addFresh(t, s, []store.EventRow{
		{Time: t0.Add(time.Hour), Kind: "request", KeyID: "kkkk1111", Country: "DE", ASOrg: "later", ASN: 3320, Status: 200},
		{Time: t0, Kind: "blocked", KeyID: "kkkk1111", Country: "DE", ASN: 3320, ASOrg: "earlier", Status: 403, BlockedCode: "address_not_allowed"},
	})
	if len(fresh) != 2 {
		t.Fatalf("fresh = %v", freshSummary(fresh))
	}
	for _, p := range fresh {
		if !p.At.Equal(t0) || p.Event != 1 {
			t.Errorf("%s place %+v: want the earliest event, index 1 at %v", p.Kind, p, t0)
		}
	}
}
