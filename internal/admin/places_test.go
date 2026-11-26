package admin_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/admin"
	"github.com/kiranscript/kman/internal/store"
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

	// Networks are a client's /24 or /64: they stay out until the install turns
	// client addresses on.
	hidden, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID})
	if err != nil || len(hidden.Places) != 3 || !hidden.NetworksWithheld {
		t.Fatalf("Places with client addresses off = %+v, %v; want the 3 non-network places and networks_withheld", hidden, err)
	}
	for _, p := range hidden.Places {
		if p.Kind == "network" {
			t.Errorf("a network got out with the setting off: %+v", p)
		}
	}
	var ae *admin.Error
	if _, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID, Kind: "network"}); !errors.As(err, &ae) || ae.Code != admin.CodeConflict || !strings.Contains(ae.Message, "expose_client_addresses") {
		t.Errorf("Places(network) with the setting off = %v, want a conflict that says how to turn it on", err)
	}

	in.exposeClients(t, true)
	got, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyID != k.ID || len(got.Places) != 5 || got.NetworksWithheld {
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
	in.exposeClients(t, true)
	got, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.Places[0]; !p.Other || p.Value != "" || p.Requests != 1 {
		t.Errorf("the most recent place = %+v, want the other row", p)
	}
}

func TestPlacesListTheClientsAKeyWasUsedByWithoutTheAddressSetting(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: t0, Kind: "request", KeyID: k.ID, ClientIP: "203.0.113.7", Client: "openai-python/3", Status: 200},
			{Time: t0.Add(time.Hour), Kind: "blocked", KeyID: k.ID, ClientIP: "198.51.100.9", Client: "curl/8", Status: 403, BlockedCode: "key_disabled"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	// A client names a program, not a person, so it is shown with the setting off,
	// unlike the networks beside it.
	got, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID, Kind: "client"})
	if err != nil || len(got.Places) != 2 || got.NetworksWithheld {
		t.Fatalf("Places(client) = %+v, %v; want the 2 clients, shown with client addresses off", got, err)
	}
	if p := got.Places[0]; p.Kind != "client" || p.Value != "curl/8" || p.Requests != 0 || p.Refused != 1 || !p.LastSeen.Equal(t0.Add(time.Hour)) {
		t.Errorf("most recent client = %+v, want curl/8, refused once", p)
	}
	if p := got.Places[1]; p.Value != "openai-python/3" || p.Requests != 1 {
		t.Errorf("the other client = %+v", p)
	}
	all, err := in.svc.Places(as("owner"), admin.PlaceQuery{ID: k.ID})
	if err != nil || len(all.Places) != 2 || !all.NetworksWithheld {
		t.Errorf("Places = %+v, %v; want the 2 clients and the networks withheld", all, err)
	}
}
