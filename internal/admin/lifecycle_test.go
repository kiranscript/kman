package admin_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

// lifecycleKey is a key named my-app on a fresh install with one credential.
func lifecycleKey(t *testing.T) (*install, admin.Key) {
	t.Helper()
	in, _ := withCredential(t)
	return in, in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
}

func registryStatus(t *testing.T, in *install, keyID string) string {
	t.Helper()
	route, ok := in.reg.Lookup(keyID)
	if !ok {
		t.Fatalf("the registry has no key %s", keyID)
	}
	return route.Key.Status
}

func TestDisablingAKeyStopsItInTheRegistryAndIsAudited(t *testing.T) {
	in, k := lifecycleKey(t)
	before := in.reg.Generation()
	in.now = in.now.Add(time.Hour)

	got, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "disabled" || got.ID != k.ID || got.Name != "my-app" {
		t.Errorf("DisableKey = %+v", got)
	}
	if s := registryStatus(t, in, k.ID); s != "disabled" {
		t.Errorf("the registry holds status %q", s)
	}
	if in.reg.Generation() <= before {
		t.Errorf("generation stayed at %d", before)
	}
	e := in.latest(t, 1)[0]
	if want := "owner key.disable key/" + k.ID; summary(e) != want {
		t.Errorf("audit = %q, want %q", summary(e), want)
	}
	if !strings.Contains(string(e.Before), `"active"`) || !strings.Contains(string(e.After), `"disabled"`) {
		t.Errorf("audit before = %s, after = %s", e.Before, e.After)
	}
	row := storedKey(t, in, k.ID)
	if !row.DisabledAt.Equal(in.now) {
		t.Errorf("disabled_at = %v, want %v", row.DisabledAt, in.now)
	}
}

func storedKey(t *testing.T, in *install, id string) store.VirtualKey {
	t.Helper()
	var row store.VirtualKey
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		var err error
		row, err = tx.Key(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestEnablingADisabledKeyLetsItWorkAgain(t *testing.T) {
	in, k := lifecycleKey(t)
	if _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: k.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := in.svc.EnableKey(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" {
		t.Errorf("EnableKey = %+v", got)
	}
	if s := registryStatus(t, in, k.ID); s != "active" {
		t.Errorf("the registry holds status %q", s)
	}
	if want := "owner key.enable key/" + k.ID; summary(in.latest(t, 1)[0]) != want {
		t.Errorf("audit = %q, want %q", summary(in.latest(t, 1)[0]), want)
	}
	if row := storedKey(t, in, k.ID); !row.DisabledAt.IsZero() {
		t.Errorf("disabled_at = %v after enabling", row.DisabledAt)
	}
}

func TestRevokingAKeyIsFinalAndKeepsItInTheRegistry(t *testing.T) {
	for _, from := range []string{"active", "disabled"} {
		t.Run(from, func(t *testing.T) {
			in, k := lifecycleKey(t)
			if from == "disabled" {
				if _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: k.ID}); err != nil {
					t.Fatal(err)
				}
			}
			in.now = in.now.Add(time.Hour)
			got, err := in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: k.ID})
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "revoked" {
				t.Errorf("RevokeKey = %+v", got)
			}
			if s := registryStatus(t, in, k.ID); s != "revoked" {
				t.Errorf("the registry holds status %q, so the gateway can't answer key_revoked", s)
			}
			e := in.latest(t, 1)[0]
			if want := "owner key.revoke key/" + k.ID; summary(e) != want {
				t.Errorf("audit = %q, want %q", summary(e), want)
			}
			if row := storedKey(t, in, k.ID); !row.RevokedAt.Equal(in.now) {
				t.Errorf("revoked_at = %v, want %v", row.RevokedAt, in.now)
			}
		})
	}
}

func TestARevokedKeyCannotBeEnabledOrDisabled(t *testing.T) {
	in, k := lifecycleKey(t)
	if _, err := in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: k.ID}); err != nil {
		t.Fatal(err)
	}
	logged := len(in.latest(t, 100))
	gen := in.reg.Generation()
	if _, err := in.svc.EnableKey(as("owner"), admin.KeyRef{ID: k.ID}); code(err) != admin.CodeConflict || !strings.Contains(err.Error(), "revoked") {
		t.Errorf("EnableKey = %v, want a conflict saying it is revoked", err)
	}
	if _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: k.ID}); code(err) != admin.CodeConflict {
		t.Errorf("DisableKey = %v, want conflict", err)
	}
	if s := registryStatus(t, in, k.ID); s != "revoked" {
		t.Errorf("the registry holds status %q", s)
	}
	if after := len(in.latest(t, 100)); after != logged || in.reg.Generation() != gen {
		t.Errorf("a refused command was audited or published")
	}
}

func TestACommandThatChangesNothingRecordsNothing(t *testing.T) {
	in, k := lifecycleKey(t)
	steps := []struct {
		name string
		run  func() (admin.Key, error)
		want string
	}{
		{"enable an active key", func() (admin.Key, error) { return in.svc.EnableKey(as("owner"), admin.KeyRef{ID: k.ID}) }, "active"},
		{"disable", func() (admin.Key, error) { return in.svc.DisableKey(as("owner"), admin.KeyRef{ID: k.ID}) }, "disabled"},
		{"disable again", func() (admin.Key, error) { return in.svc.DisableKey(as("owner"), admin.KeyRef{ID: k.ID}) }, "disabled"},
		{"revoke", func() (admin.Key, error) { return in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: k.ID}) }, "revoked"},
		{"revoke again", func() (admin.Key, error) { return in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: k.ID}) }, "revoked"},
	}
	var logged int
	var gen int64
	for _, s := range steps {
		before, genBefore := len(in.latest(t, 100)), in.reg.Generation()
		got, err := s.run()
		if err != nil || got.Status != s.want {
			t.Fatalf("%s = %+v, %v; want status %s", s.name, got, err, s.want)
		}
		logged, gen = len(in.latest(t, 100)), in.reg.Generation()
		noop := strings.HasSuffix(s.name, "again") || s.name == "enable an active key"
		if noop && (logged != before || gen != genBefore) {
			t.Errorf("%s was audited or published", s.name)
		}
		if !noop && (logged != before+1 || gen <= genBefore) {
			t.Errorf("%s was not audited and published", s.name)
		}
	}
}

func TestRevokingAKeyFreesItsName(t *testing.T) {
	in, k := lifecycleKey(t)
	if _, err := in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: k.ID}); err != nil {
		t.Fatal(err)
	}
	if again := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key; again.ID == k.ID {
		t.Errorf("the new key has the old id")
	}
}

func TestEnablingAnExpiredKeyDoesNotRevive(t *testing.T) {
	in, k := lifecycleKey(t)
	if _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: k.ID}); err != nil {
		t.Fatal(err)
	}
	in.now = k.ExpiresAt.Add(time.Second)
	got, err := in.svc.EnableKey(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || !got.Expired {
		t.Errorf("EnableKey = status %s, expired %v: it must stay expired", got.Status, got.Expired)
	}
	if route, _ := in.reg.Lookup(k.ID); !route.Key.ExpiresAt.Equal(k.ExpiresAt) {
		t.Errorf("enabling moved the expiry to %v", route.Key.ExpiresAt)
	}
}

func TestLifecycleCommandsNeedACallerAndAKnownKey(t *testing.T) {
	in, k := lifecycleKey(t)
	ref := admin.KeyRef{ID: k.ID}
	for name, run := range map[string]func(context.Context, admin.KeyRef) (admin.Key, error){
		"disable": in.svc.DisableKey, "enable": in.svc.EnableKey, "revoke": in.svc.RevokeKey,
	} {
		if _, err := run(context.Background(), ref); code(err) != admin.CodeUnauthorized {
			t.Errorf("%s with no caller = %v, want unauthorized", name, err)
		}
		if _, err := run(as("owner"), admin.KeyRef{ID: "nosuchid"}); code(err) != admin.CodeNotFound {
			t.Errorf("%s of an unknown key = %v, want not found", name, err)
		}
	}
	if s := registryStatus(t, in, k.ID); s != "active" {
		t.Errorf("the registry holds status %q", s)
	}
}
