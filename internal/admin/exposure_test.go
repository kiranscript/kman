package admin_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

// exposure is the install's exposure findings by rule, for one key.
func (in *install) exposure(t *testing.T) admin.Exposure {
	t.Helper()
	got, err := in.svc.Exposure(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func rulesOf(e admin.Exposure, keyID string) []string {
	var out []string
	for _, f := range e.Findings {
		if f.KeyID == keyID {
			out = append(out, f.Rule)
		}
	}
	return out
}

// used records one forwarded request by the key, at the time.
func (in *install) used(t *testing.T, keyID string, at time.Time) {
	t.Helper()
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{{Time: at, Kind: "request", KeyID: keyID, Status: 200}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func locked(cidr string) json.RawMessage {
	return json.RawMessage(`{"allowed_cidrs":["` + cidr + `"]}`)
}

func TestAKeyOpenToEveryAddressIsFoundWithTheCommandThatFixesIt(t *testing.T) {
	in, _ := withCredential(t)
	open := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	byNet := in.createKey(t, admin.CreateKeyRequest{Name: "by-net", Upstream: "openai", Policy: locked("203.0.113.0/24")}).Key

	got := in.exposure(t)
	if r := rulesOf(got, open.ID); len(r) != 1 || r[0] != admin.ExposureOpenToEveryAddress {
		t.Fatalf("an unlocked key's findings = %v, want %s", r, admin.ExposureOpenToEveryAddress)
	}
	if r := rulesOf(got, byNet.ID); len(r) != 0 {
		t.Errorf("a key locked to a network has findings %v", r)
	}
	f := got.Findings[0]
	if f.Name != "my-app" || !strings.HasPrefix(f.Fix, "kman key update my-app --policy '") || !strings.Contains(f.Fix, `"allowed_cidrs"`) {
		t.Errorf("finding = %+v, want a kman key update with a policy naming allowed_cidrs", f)
	}
	if got.KeysChecked != 2 {
		t.Errorf("KeysChecked = %d, want 2", got.KeysChecked)
	}
}

func TestACountryLockAloneMeansTheKeyIsNotOpenToEveryAddress(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai", Policy: json.RawMessage(`{"allowed_countries":["DE"]}`)}).Key
	if r := rulesOf(in.exposure(t), k.ID); len(r) != 0 {
		t.Errorf("a key locked to a country has findings %v", r)
	}
}

func TestTheFixForAnOpenKeyKeepsTheRestOfItsPolicy(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai", Policy: json.RawMessage(`{"daily_cap_micro":5000000}`)}).Key
	f := in.exposure(t).Findings[0]
	// The command names an unnamed key by its id, and keeps the cap: update --policy replaces the whole policy.
	if !strings.HasPrefix(f.Fix, "kman key update "+k.ID+" --policy '") || !strings.Contains(f.Fix, `"daily_cap_micro":5000000`) {
		t.Errorf("fix = %q, want it to name the key by id and keep the daily cap", f.Fix)
	}
}

func TestAFindingGoesWhenTheKeyIsFixed(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, Policy: locked("198.51.100.0/24")}); err != nil {
		t.Fatal(err)
	}
	if got := in.exposure(t); len(got.Findings) != 0 {
		t.Errorf("findings after the fix = %+v", got.Findings)
	}
}

func TestAKeyThatNeverExpiresIsFoundAndTheFixStaysInsideTheMaximumLifetime(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "old", Upstream: "openai", Policy: locked("203.0.113.0/24")}).Key
	in.sqlExec(t, `UPDATE virtual_keys SET expires_at = NULL WHERE key_id = ?`, k.ID)
	f := in.exposure(t).Findings
	if len(f) != 1 || f[0].Rule != admin.ExposureNeverExpires || f[0].Fix != "kman key update old --ttl 90d" {
		t.Fatalf("findings = %+v, want never_expires fixed by --ttl 90d (the default TTL)", f)
	}

	// Past the maximum lifetime (a year after creation) update --ttl would be refused, so the fix is to replace the key.
	in.now = t0.Add(400 * days)
	f = in.exposure(t).Findings
	if len(f) < 1 || f[0].Rule != admin.ExposureNeverExpires || !strings.HasPrefix(f[0].Fix, "kman key revoke old") {
		t.Errorf("findings = %+v, want a revoke, since the key cannot be given an expiry now", f)
	}

	// A key close to the maximum lifetime gets the time it has left.
	in.now = t0.Add(350 * days)
	f = in.exposure(t).Findings
	if len(f) < 1 || f[0].Fix != "kman key update old --ttl 15d" {
		t.Errorf("findings = %+v, want --ttl 15d, what is left of the maximum lifetime", f)
	}
}

func TestAKeyUnusedForThirtyDaysIsFoundAndAKeyUsedSinceIsNot(t *testing.T) {
	in, _ := withCredential(t)
	ttl := seconds(300 * days)
	never := in.createKey(t, admin.CreateKeyRequest{Name: "never-used", Upstream: "openai", Policy: locked("203.0.113.0/24"), TTLSeconds: ttl}).Key
	stale := in.createKey(t, admin.CreateKeyRequest{Name: "stale", Upstream: "openai", Policy: locked("203.0.113.0/24"), TTLSeconds: ttl}).Key
	busy := in.createKey(t, admin.CreateKeyRequest{Name: "busy", Upstream: "openai", Policy: locked("203.0.113.0/24"), TTLSeconds: ttl}).Key
	in.used(t, stale.ID, t0.Add(5*days))
	in.used(t, busy.ID, t0.Add(40*days))
	// A blocked attempt is not a use.
	in.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{{Time: t0.Add(44 * days), Kind: "blocked", KeyID: stale.ID, Status: 403, BlockedCode: "address_not_allowed"}})
	})

	in.now = t0.Add(29 * days)
	if got := in.exposure(t); len(got.Findings) != 0 {
		t.Errorf("29 days in, findings = %+v, want none: nothing has been idle 30 days", got.Findings)
	}
	in.now = t0.Add(45 * days)
	got := in.exposure(t)
	if r := rulesOf(got, never.ID); len(r) != 1 || r[0] != admin.ExposureUnused {
		t.Errorf("a key never used in 45 days: %v", r)
	}
	if r := rulesOf(got, stale.ID); len(r) != 1 || r[0] != admin.ExposureUnused {
		t.Errorf("a key last used 40 days ago: %v", r)
	}
	if r := rulesOf(got, busy.ID); len(r) != 0 {
		t.Errorf("a key used 5 days ago: %v", r)
	}
	for _, f := range got.Findings {
		switch f.KeyID {
		case never.ID:
			if f.LastUsedAt != nil || !strings.Contains(f.Message, "never been used") || f.Fix != "kman key disable never-used" {
				t.Errorf("never-used finding = %+v", f)
			}
		case stale.ID:
			if f.LastUsedAt == nil || !f.LastUsedAt.Equal(t0.Add(5*days)) || !strings.Contains(f.Message, "last used 2026-11-09") {
				t.Errorf("stale finding = %+v", f)
			}
		}
	}
}

func TestExposureLooksOnlyAtKeysThatWorkNow(t *testing.T) {
	in, _ := withCredential(t)
	disabled := in.createKey(t, admin.CreateKeyRequest{Name: "disabled", Upstream: "openai"}).Key
	revoked := in.createKey(t, admin.CreateKeyRequest{Name: "revoked", Upstream: "openai"}).Key
	short := in.createKey(t, admin.CreateKeyRequest{Name: "short", Upstream: "openai", TTLSeconds: seconds(24 * time.Hour)}).Key
	in.createKey(t, admin.CreateKeyRequest{Name: "live", Upstream: "openai"})
	if _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: disabled.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: revoked.ID}); err != nil {
		t.Fatal(err)
	}
	in.now = t0.Add(2 * days)
	got := in.exposure(t)
	for _, k := range []admin.Key{disabled, revoked, short} {
		if r := rulesOf(got, k.ID); len(r) != 0 {
			t.Errorf("key %s has findings %v, but it cannot be used", k.Name, r)
		}
	}
	if got.KeysChecked != 1 || len(got.Findings) != 1 || got.Findings[0].Name != "live" {
		t.Errorf("got %+v, want only the live key checked", got)
	}
}

func TestEveryRuleThatAKeyBreaksIsAFindingOfItsOwn(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "bad", Upstream: "openai", TTLSeconds: seconds(300 * days)}).Key
	in.sqlExec(t, `UPDATE virtual_keys SET expires_at = NULL WHERE key_id = ?`, k.ID)
	in.now = t0.Add(60 * days)
	r := rulesOf(in.exposure(t), k.ID)
	want := []string{admin.ExposureOpenToEveryAddress, admin.ExposureNeverExpires, admin.ExposureUnused}
	if strings.Join(r, ",") != strings.Join(want, ",") {
		t.Errorf("rules = %v, want %v", r, want)
	}
}

func TestExposureChangesNothingAndAuditsNothing(t *testing.T) {
	in, _ := withCredential(t)
	in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
	before := in.latest(t, 1)
	in.exposure(t)
	if after := in.latest(t, 1); len(after) != 1 || after[0].ID != before[0].ID {
		t.Errorf("exposure wrote an audit entry: %+v", after)
	}
}

func TestExposureNeedsACaller(t *testing.T) {
	in, _ := withCredential(t)
	if _, err := in.svc.Exposure(t.Context()); err == nil {
		t.Error("Exposure with no caller succeeded")
	}
}

func TestAnInstallWithNoKeysHasNoFindingsAndAnEmptyListNotNull(t *testing.T) {
	in := newInstall(t)
	got := in.exposure(t)
	b, _ := json.Marshal(got)
	if got.KeysChecked != 0 || !strings.Contains(string(b), `"findings":[]`) {
		t.Errorf("got %s", b)
	}
}
