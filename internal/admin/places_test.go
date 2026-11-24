package admin_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

func TestPlacesSaysWhereAKeyHasBeenUsedFromForwardedAndRefused(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: t0, Kind: "request", KeyID: k.ID, ClientIP: "203.0.113.7", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Status: 200},
			{Time: t0.Add(time.Hour), Kind: "blocked", KeyID: k.ID, ClientIP: "198.51.100.9", Country: "US", Status: 403, BlockedCode: "key_disabled"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyID != k.ID || len(got.Places) != 5 {
		t.Fatalf("Places = %+v, want the key's 5 places", got)
	}
	first := got.Places[0]
	if first.Kind != "country" || first.Value != "US" || first.Requests != 0 || first.Refused != 1 || !first.FirstSeen.Equal(t0.Add(time.Hour)) {
		t.Errorf("most recent place = %+v, want US, refused once", first)
	}
	var asn admin.Place
	for _, p := range got.Places {
		if p.Kind == "asn" {
			asn = p
		}
	}
	if asn.Value != "3320" || asn.Label != "Deutsche Telekom AG" || asn.Requests != 1 || asn.Other {
		t.Errorf("asn place = %+v", asn)
	}

	only, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID, Kind: "network"})
	if err != nil || len(only.Places) != 2 {
		t.Errorf("Places(network) = %+v, %v; want the two networks", only, err)
	}
	none := in.createKey(t, admin.CreateKeyRequest{Name: "unused", Upstream: "openai"}).Key
	if got, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: none.ID}); err != nil || got.Places == nil || len(got.Places) != 0 {
		t.Errorf("an unused key's places = %+v, %v; want an empty list", got, err)
	}
}

func TestPlacesRefusesAnUnknownKeyAndKind(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key
	var ae *admin.Error
	if _, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: "nokey123"}); !errors.As(err, &ae) || ae.Code != admin.CodeNotFound {
		t.Errorf("an unknown key: %v, want not found", err)
	}
	if _, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID, Kind: "city"}); !errors.As(err, &ae) || ae.Code != admin.CodeInvalid {
		t.Errorf("an unknown kind: %v, want invalid", err)
	}
}

func TestThePlacesPastTheLimitAreOneOtherRow(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	var batch []store.EventRow
	for i := range store.MaxPlacesPerKind + 1 {
		batch = append(batch, store.EventRow{Time: t0.Add(time.Duration(i) * time.Second), Kind: "request", KeyID: k.ID,
			ClientIP: fmt.Sprintf("10.%d.%d.1", i/256, i%256), Status: 200})
	}
	if err := in.store.Do(t.Context(), func(tx *store.Tx) error { return tx.AddEvents(batch) }); err != nil {
		t.Fatal(err)
	}
	got, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Places[0]; !p.Other || p.Value != "" || p.Requests != 1 {
		t.Errorf("the most recent place = %+v, want the other row", p)
	}
}
