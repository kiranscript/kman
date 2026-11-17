package admin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

const hour = time.Hour

// rotatable is an install with one key, my-app, and the secret it was made with.
func rotatable(t *testing.T) (*install, admin.CreatedKey) {
	t.Helper()
	in, _ := withCredential(t)
	return in, in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
}

func hashOfSecret(t *testing.T, secret string) token.Hash {
	t.Helper()
	tok, err := token.Parse(secret)
	if err != nil {
		t.Fatalf("token.Parse(%q) = %v", secret, err)
	}
	return tok.Hash
}

func (in *install) rotate(t *testing.T, id string, grace time.Duration) admin.RotatedKey {
	t.Helper()
	got, err := in.svc.RotateKey(as("owner"), admin.RotateKeyRequest{ID: id, GraceSeconds: int64(grace / time.Second)})
	if err != nil {
		t.Fatalf("RotateKey(%s, %v) = %v", id, grace, err)
	}
	return got
}

// works is whether the request path would accept secret for the key at in.now,
// as the gateway decides it: a hash the key holds, still in its grace.
func (in *install) works(t *testing.T, keyID string, secret admin.Secret) bool {
	t.Helper()
	route, ok := in.reg.Lookup(keyID)
	if !ok {
		t.Fatalf("the registry has no key %s", keyID)
	}
	h := hashOfSecret(t, string(secret))
	for _, s := range route.Key.Secrets {
		if s.Hash == h && (s.ValidUntil.IsZero() || in.now.Before(s.ValidUntil)) {
			return true
		}
	}
	return false
}

// states is the stored state of each secret of a key, by hash.
func (in *install) states(t *testing.T, keyID string) map[token.Hash]string {
	t.Helper()
	out := map[token.Hash]string{}
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		secrets, err := tx.KeySecrets(keyID)
		for _, s := range secrets {
			out[token.Hash(s.Hash)] = s.State
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRotatingAKeyGivesItANewSecretAndKeepsTheKey(t *testing.T) {
	in, created := rotatable(t)
	k := created.Key
	in.now = in.now.Add(hour)
	before, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}

	got := in.rotate(t, k.ID, 0)

	if got.Secret == "" || got.Secret == created.Secret || !strings.HasPrefix(string(got.Secret), "kmn_"+k.ID+"_") {
		t.Errorf("new secret = %q, want another kmn_%s_ key", got.Secret, k.ID)
	}
	if got.Key.ID != before.ID || got.Key.Name != before.Name || got.Key.Status != before.Status || !got.Key.ExpiresAt.Equal(before.ExpiresAt) ||
		!got.Key.CreatedAt.Equal(before.CreatedAt) || string(got.Key.Policy) != string(before.Policy) || got.Key.CredentialID != before.CredentialID {
		t.Errorf("key after = %+v\nwant like  %+v", got.Key, before)
	}
	if got.SDKEnv != "OPENAI_API_KEY" {
		t.Errorf("sdk env = %q", got.SDKEnv)
	}
	if got.PreviousValidUntil != nil {
		t.Errorf("a rotation with no grace has no previous secret, got valid until %v", got.PreviousValidUntil)
	}
	if !in.works(t, k.ID, got.Secret) || in.works(t, k.ID, created.Secret) {
		t.Errorf("new secret works = %v, old secret works = %v; want true and false", in.works(t, k.ID, got.Secret), in.works(t, k.ID, created.Secret))
	}
}

func TestRegression_F4_RotationKeepsTheKeyItsSpendItsPolicyAndItsHistory(t *testing.T) {
	in, created := rotatable(t)
	k := created.Key
	in.spend = func(keyID, _ string) admin.KeySpend {
		if keyID != k.ID {
			return admin.KeySpend{}
		}
		return admin.KeySpend{Today: admin.SpendPeriod{Period: "2026-11-04", CostMicro: 1234, Requests: 7}}
	}
	in.svc = in.service(profiles.Presets())
	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, Policy: json.RawMessage(`{}`), Note: new("billing")}); err != nil {
		t.Fatal(err)
	}
	in.now = in.now.Add(hour)
	history := len(in.latest(t, 100))

	in.rotate(t, k.ID, 0)

	got, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spend == nil || got.Spend.Today.CostMicro != 1234 || got.Spend.Today.Requests != 7 {
		t.Errorf("spend after rotation = %+v, want what the key had spent", got.Spend)
	}
	if got.Note != "billing" || got.ID != k.ID || !got.CreatedAt.Equal(k.CreatedAt) {
		t.Errorf("key after rotation = %+v", got)
	}
	entries := in.latest(t, 100)
	if len(entries) != history+1 {
		t.Fatalf("audit has %d entries, want the %d before plus the rotation", len(entries), history)
	}
	for _, e := range entries[:history] {
		if e.TargetKind == "key" && e.TargetID != k.ID {
			t.Errorf("history entry %s names another key", summary(e))
		}
	}
	keys, _ := in.svc.ListKeys(as("owner"), admin.KeyQuery{})
	if len(keys.Keys) != 1 {
		t.Errorf("%d keys after rotation, want the same one", len(keys.Keys))
	}
}

func TestDuringTheGraceBothSecretsWorkAndAfterItOnlyTheNewOne(t *testing.T) {
	in, created := rotatable(t)
	k := created.Key
	in.now = in.now.Add(hour)

	got := in.rotate(t, k.ID, 2*hour)

	if want := in.now.Add(2 * hour); got.PreviousValidUntil == nil || !got.PreviousValidUntil.Equal(want) {
		t.Errorf("previous valid until = %v, want %v", got.PreviousValidUntil, want)
	}
	if !in.works(t, k.ID, created.Secret) || !in.works(t, k.ID, got.Secret) {
		t.Error("both secrets should work during the grace")
	}
	in.now = in.now.Add(2*hour - time.Second)
	if !in.works(t, k.ID, created.Secret) {
		t.Error("the previous secret should work until the grace ends")
	}
	in.now = in.now.Add(time.Second)
	if in.works(t, k.ID, created.Secret) {
		t.Error("the previous secret works after the grace")
	}
	if !in.works(t, k.ID, got.Secret) {
		t.Error("the new secret stopped working")
	}
}

func TestAfterTheGraceThePreviousSecretBecomesARetiredOneThatNeverWorks(t *testing.T) {
	in, created := rotatable(t)
	k := created.Key
	got := in.rotate(t, k.ID, hour)
	old, cur := hashOfSecret(t, string(created.Secret)), hashOfSecret(t, string(got.Secret))
	if s := in.states(t, k.ID); s[old] != "previous" || s[cur] != "current" || len(s) != 2 {
		t.Fatalf("during the grace the key holds %v", s)
	}

	in.now = in.now.Add(hour)
	next, err := in.svc.SettleSecrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !next.IsZero() {
		t.Errorf("next grace end = %v, want none", next)
	}
	if s := in.states(t, k.ID); s[old] != "retired" || s[cur] != "current" || len(s) != 2 {
		t.Errorf("after the grace the key holds %v", s)
	}
	if in.works(t, k.ID, created.Secret) {
		t.Error("a retired secret works")
	}

	in.now = in.now.Add(30 * 24 * hour)
	if _, err := in.svc.SettleSecrets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := in.states(t, k.ID); len(s) != 1 || s[cur] != "current" {
		t.Errorf("30 days on, the key holds %v, want only the current secret", s)
	}
}

func TestASecondRotationInsideTheGraceEndsTheOlderSecretAtOnce(t *testing.T) {
	in, created := rotatable(t)
	k := created.Key
	second := in.rotate(t, k.ID, 24*hour)
	in.now = in.now.Add(hour)
	third := in.rotate(t, k.ID, 24*hour)

	if in.works(t, k.ID, created.Secret) {
		t.Error("the oldest secret still works after a second rotation")
	}
	if !in.works(t, k.ID, second.Secret) || !in.works(t, k.ID, third.Secret) {
		t.Error("the two newest secrets should work")
	}
	route, _ := in.reg.Lookup(k.ID)
	if len(route.Key.Secrets) > 2 {
		t.Errorf("the registry holds %d secrets, want at most two", len(route.Key.Secrets))
	}
	states := in.states(t, k.ID)
	if states[hashOfSecret(t, string(created.Secret))] != "retired" || len(states) != 3 {
		t.Errorf("the key holds %v, want the oldest retired", states)
	}
}

func TestAFailedRotationLeavesTheKeyExactlyAsItWas(t *testing.T) {
	in, _ := withCredential(t)
	// A source that always gives the same bytes makes a rotation draw the secret
	// the key already has, which the store refuses after it has moved the old
	// secrets: the whole rotation must roll back.
	in.rand = constantReader(7)
	in.svc = in.service(profiles.Presets())
	created := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
	k := created.Key
	statesBefore := in.states(t, k.ID)
	routeBefore, _ := in.reg.Lookup(k.ID)
	auditBefore, generationBefore := len(in.latest(t, 100)), in.reg.Generation()

	_, err := in.svc.RotateKey(as("owner"), admin.RotateKeyRequest{ID: k.ID, GraceSeconds: int64(hour / time.Second)})
	if code(err) != admin.CodeConflict {
		t.Fatalf("RotateKey = %v, want a conflict from the store", err)
	}

	if after := in.states(t, k.ID); len(after) != 1 || after[hashOfSecret(t, string(created.Secret))] != "current" || len(statesBefore) != 1 {
		t.Errorf("the key holds %v, want only its current secret", after)
	}
	routeAfter, _ := in.reg.Lookup(k.ID)
	if routeAfter.Key != routeBefore.Key || in.reg.Generation() != generationBefore {
		t.Error("the registry changed")
	}
	if len(in.latest(t, 100)) != auditBefore {
		t.Error("a failed rotation was audited")
	}
	if !in.works(t, k.ID, created.Secret) {
		t.Error("the key's secret stopped working")
	}
}

type constantReader byte

func (c constantReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(c)
	}
	return len(p), nil
}

func TestRotationRefusesWhatCannotBeRotated(t *testing.T) {
	in, created := rotatable(t)
	k := created.Key
	for _, tc := range []struct {
		name  string
		req   admin.RotateKeyRequest
		setup func()
		want  admin.Code
	}{
		{"a key that is not there", admin.RotateKeyRequest{ID: "nokey123"}, nil, admin.CodeNotFound},
		{"a negative grace", admin.RotateKeyRequest{ID: k.ID, GraceSeconds: -1}, nil, admin.CodeInvalid},
		{"a grace over 30 days", admin.RotateKeyRequest{ID: k.ID, GraceSeconds: 31 * 24 * 3600}, nil, admin.CodeInvalid},
		{"a revoked key", admin.RotateKeyRequest{ID: k.ID}, func() {
			if _, err := in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: k.ID}); err != nil {
				t.Fatal(err)
			}
		}, admin.CodeConflict},
	} {
		if tc.setup != nil {
			tc.setup()
		}
		if _, err := in.svc.RotateKey(as("owner"), tc.req); code(err) != tc.want {
			t.Errorf("rotating %s = %v, want code %q", tc.name, err, tc.want)
		}
	}
	if !in.works(t, k.ID, created.Secret) {
		t.Error("a refused rotation changed the key's secret")
	}
}

func TestRotatingADisabledKeyLeavesItDisabled(t *testing.T) {
	in, created := rotatable(t)
	if _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: created.Key.ID}); err != nil {
		t.Fatal(err)
	}
	got := in.rotate(t, created.Key.ID, 0)
	if got.Key.Status != "disabled" {
		t.Errorf("status = %q, want disabled", got.Key.Status)
	}
	if route, _ := in.reg.Lookup(created.Key.ID); route.Key.Status != "disabled" {
		t.Errorf("the registry holds status %q", route.Key.Status)
	}
}

func TestARotationIsAuditedWithItsGraceAndNoSecret(t *testing.T) {
	in, created := rotatable(t)
	got := in.rotate(t, created.Key.ID, 3*hour)
	e := in.latest(t, 1)[0]
	if want := "owner key.rotate key/" + created.Key.ID; summary(e) != want {
		t.Fatalf("audit = %q, want %q", summary(e), want)
	}
	var after struct {
		Grace int64 `json:"grace_seconds"`
	}
	if err := json.Unmarshal(e.After, &after); err != nil || after.Grace != 3*3600 {
		t.Errorf("after = %s, want a grace of 10800 seconds (%v)", string(e.After), err)
	}
	for _, secret := range []admin.Secret{created.Secret, got.Secret} {
		if strings.Contains(string(e.Before)+string(e.After), string(secret)) || strings.Contains(string(e.After), strings.Split(string(secret), "_")[2]) {
			t.Errorf("the audit entry holds a secret: %s", string(e.After))
		}
	}
}

func TestTheGraceEndsOnTimeWithNoOneAskingAndARotationWakesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in, created := rotatable(t)
		in.clock = time.Now
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { in.svc.RunSettling(ctx); close(done) }()
		synctest.Wait()

		// The loop is idle: no grace is running. A rotation with a grace of
		// ten minutes must wake it, and ten minutes on the secret is retired.
		got := in.rotate(t, created.Key.ID, 10*time.Minute)
		old := hashOfSecret(t, string(created.Secret))
		synctest.Wait()
		if s := in.states(t, created.Key.ID); s[old] != "previous" {
			t.Fatalf("during the grace the old secret is %q", s[old])
		}
		time.Sleep(10*time.Minute + time.Second)
		synctest.Wait()
		if s := in.states(t, created.Key.ID); s[old] != "retired" || s[hashOfSecret(t, string(got.Secret))] != "current" {
			t.Errorf("after the grace the key holds %v", s)
		}
		cancel()
		<-done
	})
}
