package admin_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/admin"
	"github.com/kiranscript/kman/internal/store"
)

// seenFrom records requests by a key from an address, at the time.
func (in *install) seenFrom(t *testing.T, keyID, ip string, at time.Time, kinds ...string) {
	t.Helper()
	var rows []store.EventRow
	for _, kind := range kinds {
		rows = append(rows, store.EventRow{Time: at, Kind: kind, KeyID: keyID, ClientIP: ip, Country: "DE", ASN: 3320, Status: 200, CostMicro: 1000, CostKnown: kind == "request"})
	}
	if err := in.store.Do(t.Context(), func(tx *store.Tx) error { return tx.AddEvents(rows) }); err != nil {
		t.Fatal(err)
	}
}

func (in *install) exposeClients(t *testing.T, on bool) {
	t.Helper()
	if _, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{ExposeClientAddresses: &on}); err != nil {
		t.Fatal(err)
	}
}

func TestClientAddressesStayOutOfTheAPIUntilTheOperatorTurnsThemOn(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	in.seenFrom(t, k.ID, "203.0.113.7", t0, "request")

	if got := mustSettings(t, in); got.ExposeClientAddresses {
		t.Fatal("expose_client_addresses is on by default; it must be off")
	}
	_, err := in.svc.KeyClients(as("owner"), admin.KeyRef{ID: k.ID})
	if code(err) != admin.CodeConflict || !strings.Contains(err.Error(), "kman settings set expose_client_addresses true") {
		t.Fatalf("KeyClients with the setting off = %v, want a conflict that says how to turn it on", err)
	}

	in.exposeClients(t, true)
	got, err := in.svc.KeyClients(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil || got.Total != 1 || len(got.Clients) != 1 || got.Clients[0].Address != "203.0.113.7" {
		t.Fatalf("KeyClients with it on = %+v, %v", got, err)
	}

	// Turning it off again closes it, and both changes are in the audit log.
	in.exposeClients(t, false)
	if _, err := in.svc.KeyClients(as("owner"), admin.KeyRef{ID: k.ID}); code(err) != admin.CodeConflict {
		t.Errorf("KeyClients after turning it off = %v, want a conflict", err)
	}
	var sets []string
	for _, e := range in.latest(t, 100) {
		if e.Action == "settings.set" && e.TargetID == "expose_client_addresses" {
			sets = append(sets, e.TargetID)
		}
	}
	if len(sets) != 2 {
		t.Errorf("audited %d changes of expose_client_addresses, want 2", len(sets))
	}
}

func TestAKeysClientsAreGroupedByAddressWithWhatEachDid(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	other := in.createKey(t, admin.CreateKeyRequest{Name: "other", Upstream: "openai"}).Key
	in.exposeClients(t, true)
	in.seenFrom(t, k.ID, "203.0.113.7", t0.Add(-2*time.Hour), "request", "request", "blocked")
	in.seenFrom(t, k.ID, "198.51.100.9", t0.Add(-time.Hour), "request")
	in.seenFrom(t, other.ID, "192.0.2.1", t0, "request")

	got, err := in.svc.KeyClients(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyID != k.ID || got.Total != 2 || len(got.Clients) != 2 {
		t.Fatalf("KeyClients = %+v", got)
	}
	if got.Clients[0].Address != "198.51.100.9" || got.Clients[1].Address != "203.0.113.7" {
		t.Errorf("order = %s, %s; want the most recently seen first", got.Clients[0].Address, got.Clients[1].Address)
	}
	a := got.Clients[1]
	if a.Requests != 2 || a.Blocked != 1 || a.CostMicro != 2000 || a.Country != "DE" || a.ASN != 3320 {
		t.Errorf("203.0.113.7 = %+v", a)
	}
}

func TestAKeyThatHasNeverBeenUsedHasNoClientsAndAnUnknownKeyIsNotFound(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	in.exposeClients(t, true)
	got, err := in.svc.KeyClients(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil || got.Total != 0 || got.Clients == nil || len(got.Clients) != 0 {
		t.Errorf("KeyClients of an unused key = %+v, %v; want an empty list, not null", got, err)
	}
	if _, err := in.svc.KeyClients(as("owner"), admin.KeyRef{ID: "k_nothing"}); code(err) != admin.CodeNotFound {
		t.Errorf("KeyClients of an unknown key = %v, want not found", err)
	}
}

func mustSettings(t *testing.T, in *install) admin.Settings {
	t.Helper()
	s, err := in.svc.Settings(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
