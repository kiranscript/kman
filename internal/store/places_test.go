package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/store"
)

func places(t *testing.T, s *store.Store, key, kind string) []store.PlaceRow {
	t.Helper()
	var out []store.PlaceRow
	do(t, s, func(tx *store.Tx) (err error) { out, err = tx.Places(key, kind); return })
	return out
}

func TestEventsRecordEachPlaceOfTheirKeyWithFirstAndLastSeen(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: t0, Kind: "request", KeyID: "kkkk1111", ClientIP: "203.0.113.7", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Status: 200},
			{Time: t0.Add(time.Minute), Kind: "request", KeyID: "kkkk1111", ClientIP: "203.0.113.200", Country: "DE", ASN: 3320, Status: 200},
			{Time: t0.Add(2 * time.Minute), Kind: "blocked", KeyID: "kkkk1111", ClientIP: "2001:db8:1:2:3::9", Country: "US", ASN: 15169, ASOrg: "Google LLC", Status: 403, BlockedCode: "address_not_allowed"},
			{Time: t0.Add(3 * time.Minute), Kind: "blocked", ClientIP: "198.51.100.1", Country: "FR", Status: 401, BlockedCode: "invalid_key"}, // no key: no place
			{Time: t0.Add(4 * time.Minute), Kind: "request", KeyID: "kkkk2222", Status: 200},                                                   // a Unix socket: no address, no place
		})
	})
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: t0.Add(time.Hour), Kind: "request", KeyID: "kkkk1111", ClientIP: "::ffff:203.0.113.9", Country: "DE", ASN: 3320, Status: 200},
		})
	})

	got := places(t, s, "kkkk1111", "")
	want := []store.PlaceRow{
		{KeyID: "kkkk1111", Kind: "asn", Value: "3320", Label: "Deutsche Telekom AG", FirstSeen: t0, LastSeen: t0.Add(time.Hour), Count: 3},
		{KeyID: "kkkk1111", Kind: "country", Value: "DE", FirstSeen: t0, LastSeen: t0.Add(time.Hour), Count: 3},
		{KeyID: "kkkk1111", Kind: "network", Value: "203.0.113.0/24", FirstSeen: t0, LastSeen: t0.Add(time.Hour), Count: 3},
		{KeyID: "kkkk1111", Kind: "asn", Value: "15169", Label: "Google LLC", FirstSeen: t0.Add(2 * time.Minute), LastSeen: t0.Add(2 * time.Minute), Blocked: 1},
		{KeyID: "kkkk1111", Kind: "country", Value: "US", FirstSeen: t0.Add(2 * time.Minute), LastSeen: t0.Add(2 * time.Minute), Blocked: 1},
		{KeyID: "kkkk1111", Kind: "network", Value: "2001:db8:1:2::/64", FirstSeen: t0.Add(2 * time.Minute), LastSeen: t0.Add(2 * time.Minute), Blocked: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("Places =\n%+v\nwant\n%+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("place %d = %+v\nwant      %+v", i, got[i], want[i])
		}
	}

	if got := places(t, s, "kkkk1111", "network"); len(got) != 2 || got[0].Kind != "network" || got[1].Kind != "network" {
		t.Errorf("Places(network) = %+v, want the two networks", got)
	}
	if got := places(t, s, "kkkk2222", ""); len(got) != 0 {
		t.Errorf("a key used only over a Unix socket has places %+v, want none", got)
	}
}

func TestALaterLabelReplacesAndAMissingOneKeepsTheLast(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	for i, org := range []string{"Old Name", "", "New Name"} {
		do(t, s, func(tx *store.Tx) error {
			return tx.AddEvents([]store.EventRow{{Time: t0.Add(time.Duration(i) * time.Minute), Kind: "request", KeyID: "kkkk1111", ASN: 64500, ASOrg: org, Status: 200}})
		})
		if i == 1 {
			if got := places(t, s, "kkkk1111", "asn"); got[0].Label != "Old Name" {
				t.Errorf("after an event with no label, label = %q, want the last one kept", got[0].Label)
			}
		}
	}
	if got := places(t, s, "kkkk1111", "asn"); got[0].Label != "New Name" {
		t.Errorf("label = %q, want the latest", got[0].Label)
	}
}

func TestAnOlderEventDoesNotMoveLastSeenBack(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{{Time: t0, Kind: "request", KeyID: "kkkk1111", Country: "DE", Status: 200}})
	})
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{{Time: t0.Add(-time.Hour), Kind: "request", KeyID: "kkkk1111", Country: "DE", Status: 200}})
	})
	got := places(t, s, "kkkk1111", "country")
	if len(got) != 1 || !got[0].FirstSeen.Equal(t0.Add(-time.Hour)) || !got[0].LastSeen.Equal(t0) || got[0].Count != 2 {
		t.Errorf("place = %+v, want first seen an hour earlier, last seen at t0, count 2", got)
	}
}

func TestAKeyKeepsAtMostMaxPlacesPerKindAndCountsTheRestAsOther(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	// One more network than a key keeps, then one of the kept ones again, and two
	// more new ones in a later batch.
	var batch []store.EventRow
	for i := range store.MaxPlacesPerKind + 1 {
		batch = append(batch, store.EventRow{Time: t0.Add(time.Duration(i) * time.Second), Kind: "request", KeyID: "kkkk1111",
			ClientIP: fmt.Sprintf("10.%d.%d.1", i/256, i%256), Status: 200})
	}
	batch = append(batch, store.EventRow{Time: t0.Add(time.Hour), Kind: "request", KeyID: "kkkk1111", ClientIP: "10.0.0.1", Status: 200})
	do(t, s, func(tx *store.Tx) error { return tx.AddEvents(batch) })
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: t0.Add(2 * time.Hour), Kind: "request", KeyID: "kkkk1111", ClientIP: "172.16.0.1", Status: 200},
			{Time: t0.Add(2 * time.Hour), Kind: "blocked", KeyID: "kkkk1111", ClientIP: "172.16.1.1", Status: 403, BlockedCode: "key_disabled"},
		})
	})

	got := places(t, s, "kkkk1111", "network")
	if len(got) != store.MaxPlacesPerKind+1 {
		t.Fatalf("%d networks, want %d kept and one other", len(got), store.MaxPlacesPerKind)
	}
	var other *store.PlaceRow
	for i := range got {
		if got[i].Value == store.PlaceOther {
			other = &got[i]
		}
		if got[i].Value == "10.3.232.0/24" { // the 1001st
			t.Errorf("the place past the limit was kept: %+v", got[i])
		}
	}
	if other == nil || other.Count != 2 || other.Blocked != 1 || !other.FirstSeen.Equal(t0.Add(store.MaxPlacesPerKind*time.Second)) || !other.LastSeen.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("other = %+v, want 2 forwarded and 1 refused, from the 1001st on", other)
	}
	if got[0].Value != store.PlaceOther && got[0].Value != "172.16.0.0/24" {
		t.Errorf("the most recent place = %+v", got[0])
	}
	for _, p := range got {
		if p.Value == "10.0.0.0/24" && p.Count != 2 {
			t.Errorf("a kept place seen again past the limit has count %d, want 2", p.Count)
		}
	}
	if got := places(t, s, "kkkk1111", "country"); len(got) != 0 {
		t.Errorf("the limit on networks touched countries: %+v", got)
	}
}

func TestEventsRecordTheClientsOfTheirKeyAsPlaces(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	do(t, s, func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: t0, Kind: "request", KeyID: "kkkk1111", Client: "openai-python/3", Status: 200},
			{Time: t0.Add(time.Minute), Kind: "request", KeyID: "kkkk1111", Client: "openai-python/3", Status: 200},
			{Time: t0.Add(2 * time.Minute), Kind: "blocked", KeyID: "kkkk1111", Client: "curl/8", Status: 403, BlockedCode: "address_not_allowed"},
			{Time: t0.Add(3 * time.Minute), Kind: "blocked", KeyID: "kkkk1111", Client: "none", Status: 403, BlockedCode: "key_disabled"},
			{Time: t0.Add(4 * time.Minute), Kind: "blocked", Client: "curl/8", Status: 401, BlockedCode: "invalid_key"}, // no key: no place
			{Time: t0.Add(5 * time.Minute), Kind: "request", KeyID: "kkkk1111", Status: 200},                            // from before clients were recorded
		})
	})
	got := places(t, s, "kkkk1111", "client")
	want := []store.PlaceRow{
		{KeyID: "kkkk1111", Kind: "client", Value: "none", FirstSeen: t0.Add(3 * time.Minute), LastSeen: t0.Add(3 * time.Minute), Blocked: 1},
		{KeyID: "kkkk1111", Kind: "client", Value: "curl/8", FirstSeen: t0.Add(2 * time.Minute), LastSeen: t0.Add(2 * time.Minute), Blocked: 1},
		{KeyID: "kkkk1111", Kind: "client", Value: "openai-python/3", FirstSeen: t0, LastSeen: t0.Add(time.Minute), Count: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("Places(client) = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("place %d = %+v\nwant      %+v", i, got[i], want[i])
		}
	}
	if all := places(t, s, "kkkk1111", ""); len(all) != 3 {
		t.Errorf("an event with no client or no address made places: %+v", all)
	}
}

func TestAKeyKeepsAtMostMaxPlacesPerKindOfClientsToo(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	var batch []store.EventRow
	for i := range store.MaxPlacesPerKind + 5 {
		batch = append(batch, store.EventRow{Time: t0.Add(time.Duration(i) * time.Second), Kind: "request", KeyID: "kkkk1111",
			Client: fmt.Sprintf("app-%d/1", i), Status: 200})
	}
	do(t, s, func(tx *store.Tx) error { return tx.AddEvents(batch) })
	got := places(t, s, "kkkk1111", "client")
	if len(got) != store.MaxPlacesPerKind+1 {
		t.Fatalf("%d clients, want %d kept and one other", len(got), store.MaxPlacesPerKind)
	}
	var other *store.PlaceRow
	for i := range got {
		if got[i].Value == store.PlaceOther {
			other = &got[i]
		}
	}
	if other == nil || other.Count != 5 {
		t.Errorf("other = %+v, want the 5 clients past the limit", other)
	}
}
