package admin_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
)

const secret = "sk-live-sentinel-0123456789abcdefWXYZ"

// withOpenAI is an install with an upstream named openai.
func withOpenAI(t *testing.T) *install {
	t.Helper()
	in := newInstall(t)
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai"}); err != nil {
		t.Fatal(err)
	}
	return in
}

func (in *install) addCredential(t *testing.T, name, upstream string) admin.Credential {
	t.Helper()
	c, err := in.svc.AddCredential(as("owner"), admin.AddCredentialRequest{Name: name, Upstream: upstream, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestACredentialIsSealedCachedAndPublished(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	if !strings.HasPrefix(c.ID, "cr_") || c.Name != "main" || c.Upstream != "openai" || c.Last4 != "WXYZ" || !c.CreatedAt.Equal(t0) || c.RotatedAt != nil {
		t.Errorf("AddCredential = %+v", c)
	}
	// The vault holds the plaintext for the request path.
	plain, ok := in.vault.Credential(c.ID)
	if !ok || string(plain) != secret {
		t.Errorf("the vault holds %q, %v", plain, ok)
	}
	// The registry names it, and its upstream.
	rc, ok := in.reg.Credential(c.ID)
	if !ok || rc.Name != "main" || rc.UpstreamID != c.UpstreamID || rc.Generation != in.reg.Generation() {
		t.Errorf("the registry holds %+v, %v", rc, ok)
	}
	// The store holds only ciphertext, bound to the id and the data key.
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		row, err := tx.Credential(c.ID)
		if err != nil {
			return err
		}
		if strings.Contains(string(row.Ciphertext), secret) || len(row.Ciphertext) == 0 || row.DataKeyID != in.vault.DataKeyID() {
			t.Errorf("the row holds ciphertext %q under %s", row.Ciphertext, row.DataKeyID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAddingACredentialIsAudited(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	e := in.latest(t, 1)[0]
	if want := "owner credential.create credential/" + c.ID; summary(e) != want {
		t.Errorf("audit = %s, want %s", summary(e), want)
	}
	if !strings.Contains(string(e.After), `"name":"main"`) || strings.Contains(string(e.After), secret) {
		t.Errorf("the entry records %s", e.After)
	}
}

func TestACredentialIsNamedForItsUpstreamByNameOrId(t *testing.T) {
	in := withOpenAI(t)
	u, err := in.svc.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	c := in.addCredential(t, "main", u.ID)
	if c.UpstreamID != u.ID || c.Upstream != "openai" {
		t.Errorf("credential on %+v", c)
	}
}

func TestShowingAnUpstreamListsItsCredentialsWithoutTheirSecrets(t *testing.T) {
	in := withOpenAI(t)
	in.addCredential(t, "zulu", "openai")
	in.addCredential(t, "alpha", "openai")
	u, err := in.svc.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range u.Credentials {
		names = append(names, c.Name)
	}
	if want := []string{"alpha", "zulu"}; !slices.Equal(names, want) {
		t.Errorf("credentials = %v, want %v", names, want)
	}
	list, err := in.svc.ListUpstreams(as("owner"))
	if err != nil || len(list.Upstreams[0].Credentials) != 2 {
		t.Errorf("ListUpstreams = %+v, %v", list, err)
	}
}

func TestABadCredentialIsRefusedWithoutEchoingTheSecret(t *testing.T) {
	in := withOpenAI(t)
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "twilio", Preset: "twilio"}); err != nil {
		t.Fatal(err)
	}
	in.addCredential(t, "taken", "openai")
	before := len(in.latest(t, 100))
	for _, tc := range []struct {
		name string
		req  admin.AddCredentialRequest
		want admin.Code
	}{
		{"no secret", admin.AddCredentialRequest{Name: "x", Upstream: "openai"}, admin.CodeInvalid},
		{"a bad name", admin.AddCredentialRequest{Name: "Not OK", Upstream: "openai", Secret: secret}, admin.CodeInvalid},
		{"no upstream", admin.AddCredentialRequest{Name: "x", Upstream: "nope", Secret: secret}, admin.CodeNotFound},
		{"a taken name", admin.AddCredentialRequest{Name: "taken", Upstream: "openai", Secret: secret}, admin.CodeConflict},
		{"a line break", admin.AddCredentialRequest{Name: "x", Upstream: "openai", Secret: secret + "\r\nX-Evil: 1"}, admin.CodeInvalid},
		{"a huge secret", admin.AddCredentialRequest{Name: "x", Upstream: "openai", Secret: admin.Secret(strings.Repeat("s", 70000))}, admin.CodeInvalid},
		{"basic with no colon", admin.AddCredentialRequest{Name: "x", Upstream: "twilio", Secret: secret}, admin.CodeInvalid},
		{"basic with no user", admin.AddCredentialRequest{Name: "x", Upstream: "twilio", Secret: ":" + secret}, admin.CodeInvalid},
	} {
		_, err := in.svc.AddCredential(as("owner"), tc.req)
		if code(err) != tc.want {
			t.Errorf("%s: AddCredential = %v, want %s", tc.name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the error holds the secret: %v", tc.name, err)
		}
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("the audit log grew by %d", after-before)
	}
}

func TestABasicCredentialTakesUserPasswordOrAPlainSecretWithAUsername(t *testing.T) {
	in := newInstall(t)
	for _, tc := range []struct {
		upstream admin.AddUpstreamRequest
		secret   admin.Secret
	}{
		{admin.AddUpstreamRequest{Name: "a", Preset: "twilio"}, "user:password"},
		{admin.AddUpstreamRequest{Name: "b", UpstreamSettings: admin.UpstreamSettings{
			Host: ptr("b.example.com"), Paths: ptr([]string{"GET /"}), Auth: &admin.Auth{Style: "basic", BasicUsername: "api"},
		}}, "just-a-password"},
	} {
		if _, err := in.svc.AddUpstream(as("owner"), tc.upstream); err != nil {
			t.Fatal(err)
		}
		if _, err := in.svc.AddCredential(as("owner"), admin.AddCredentialRequest{Name: tc.upstream.Name, Upstream: tc.upstream.Name, Secret: tc.secret}); err != nil {
			t.Errorf("%s: %v", tc.upstream.Name, err)
		}
	}
}

// A secret too short to spare four characters shows none of them.
func TestAShortSecretShowsNoLastFour(t *testing.T) {
	in := withOpenAI(t)
	c, err := in.svc.AddCredential(as("owner"), admin.AddCredentialRequest{Name: "short", Upstream: "openai", Secret: "abcd1234"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Last4 != "" {
		t.Errorf("last4 = %q", c.Last4)
	}
}

func TestCredentialNamesAreUniqueAcrossUpstreamsAndReusableAfterDelete(t *testing.T) {
	in := withOpenAI(t)
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "groq", Preset: "groq"}); err != nil {
		t.Fatal(err)
	}
	in.addCredential(t, "main", "openai")
	if _, err := in.svc.AddCredential(as("owner"), admin.AddCredentialRequest{Name: "main", Upstream: "groq", Secret: secret}); code(err) != admin.CodeConflict {
		t.Errorf("the same name on another upstream = %v, want conflict", err)
	}
	if _, err := in.svc.RemoveCredential(as("owner"), admin.CredentialRef{Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	in.addCredential(t, "main", "groq")
}

func TestUpdatingASecretReachesTheVaultAndWipesTheOldOne(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	old, _ := in.vault.Credential(c.ID)
	in.now = t0.Add(time.Minute)

	const replaced = "sk-live-replacement-0123456789abcdefQRST"
	got, err := in.svc.UpdateCredential(as("ci"), admin.CredentialUpdate{Ref: "main", Secret: ptr(admin.Secret(replaced))})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != c.ID || got.Last4 != "QRST" || got.RotatedAt == nil || !got.RotatedAt.Equal(in.now) {
		t.Errorf("UpdateCredential = %+v", got)
	}
	if plain, _ := in.vault.Credential(c.ID); string(plain) != replaced {
		t.Errorf("the vault holds %q", plain)
	}
	if strings.Contains(string(old), "sk-live") {
		t.Errorf("the old plaintext is still in memory: %q", old)
	}
	e := in.latest(t, 1)[0]
	if want := "ci credential.update credential/" + c.ID; summary(e) != want {
		t.Errorf("audit = %s, want %s", summary(e), want)
	}
	if blob := string(e.Before) + string(e.After); strings.Contains(blob, secret) || strings.Contains(blob, replaced) {
		t.Errorf("the entry holds a secret: %s", blob)
	}
}

func TestRenamingACredentialLeavesItsSecret(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	got, err := in.svc.UpdateCredential(as("owner"), admin.CredentialUpdate{Ref: c.ID, Name: ptr("primary")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "primary" || got.RotatedAt != nil || got.Last4 != c.Last4 {
		t.Errorf("UpdateCredential = %+v", got)
	}
	if plain, _ := in.vault.Credential(c.ID); string(plain) != secret {
		t.Errorf("the vault holds %q", plain)
	}
	if rc, _ := in.reg.Credential(c.ID); rc.Name != "primary" {
		t.Errorf("the registry holds %+v", rc)
	}
}

func TestABadCredentialUpdateIsRefused(t *testing.T) {
	in := withOpenAI(t)
	in.addCredential(t, "main", "openai")
	in.addCredential(t, "other", "openai")
	for _, tc := range []struct {
		name string
		req  admin.CredentialUpdate
		want admin.Code
	}{
		{"nothing", admin.CredentialUpdate{Ref: "main"}, admin.CodeInvalid},
		{"missing", admin.CredentialUpdate{Ref: "nope", Name: ptr("x")}, admin.CodeNotFound},
		{"a taken name", admin.CredentialUpdate{Ref: "main", Name: ptr("other")}, admin.CodeConflict},
		{"a bad name", admin.CredentialUpdate{Ref: "main", Name: ptr("Bad Name")}, admin.CodeInvalid},
		{"an empty secret", admin.CredentialUpdate{Ref: "main", Secret: ptr(admin.Secret(""))}, admin.CodeInvalid},
	} {
		if _, err := in.svc.UpdateCredential(as("owner"), tc.req); code(err) != tc.want {
			t.Errorf("%s: UpdateCredential = %v, want %s", tc.name, err, tc.want)
		}
	}
}

func TestRemovingACredentialWipesItEverywhere(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	plain, _ := in.vault.Credential(c.ID)
	if _, err := in.svc.RemoveCredential(as("owner"), admin.CredentialRef{Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := in.vault.Credential(c.ID); ok {
		t.Error("the vault still holds it")
	}
	if strings.Contains(string(plain), "sk-live") {
		t.Errorf("the plaintext is still in memory: %q", plain)
	}
	if _, ok := in.reg.Credential(c.ID); ok {
		t.Error("the registry still holds it")
	}
	if want := "owner credential.delete credential/" + c.ID; summary(in.latest(t, 1)[0]) != want {
		t.Errorf("audit = %s, want %s", summary(in.latest(t, 1)[0]), want)
	}
	if _, err := in.svc.RemoveCredential(as("owner"), admin.CredentialRef{Ref: "main"}); code(err) != admin.CodeNotFound {
		t.Errorf("a second remove = %v, want not found", err)
	}
}

func TestDeleteIsRefusedWhileAKeyDependsOnTheItem(t *testing.T) {
	for _, status := range []string{"active", "disabled"} {
		t.Run(status, func(t *testing.T) {
			in := withOpenAI(t)
			c := in.addCredential(t, "main", "openai")
			in.seedKey(t, "abcd1234", "my-app", c.ID, status)
			before := len(in.latest(t, 100))

			_, err := in.svc.RemoveCredential(as("owner"), admin.CredentialRef{Ref: "main"})
			if code(err) != admin.CodeConflict || !strings.Contains(err.Error(), "abcd1234") {
				t.Errorf("RemoveCredential = %v, want conflict naming the key", err)
			}
			_, err = in.svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "openai"})
			if code(err) != admin.CodeConflict || !strings.Contains(err.Error(), "abcd1234") {
				t.Errorf("RemoveUpstream = %v, want conflict naming the key", err)
			}
			if _, ok := in.vault.Credential(c.ID); !ok {
				t.Error("the refused delete wiped the vault")
			}
			if _, ok := in.reg.Credential(c.ID); !ok {
				t.Error("the refused delete changed the registry")
			}
			if after := len(in.latest(t, 100)); after != before {
				t.Errorf("the audit log grew by %d", after-before)
			}
		})
	}
}

func TestARevokedKeyDoesNotHoldADelete(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	in.seedKey(t, "abcd1234", "my-app", c.ID, "revoked")
	if _, err := in.svc.RemoveCredential(as("owner"), admin.CredentialRef{Ref: "main"}); err != nil {
		t.Errorf("RemoveCredential = %v", err)
	}
	if _, err := in.svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "openai"}); err != nil {
		t.Errorf("RemoveUpstream = %v", err)
	}
}

func TestARefusalNamesAFewKeysAndCountsTheRest(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	for i := range 8 {
		in.seedKey(t, fmt.Sprintf("key%05d", i), fmt.Sprintf("app-%d", i), c.ID, "active")
	}
	_, err := in.svc.RemoveCredential(as("owner"), admin.CredentialRef{Ref: "main"})
	if code(err) != admin.CodeConflict || !strings.Contains(err.Error(), "8 virtual keys") || strings.Contains(err.Error(), "key00007") {
		t.Errorf("RemoveCredential = %v", err)
	}
}

func TestRemovingAnUpstreamRemovesItsCredentials(t *testing.T) {
	in := withOpenAI(t)
	a, b := in.addCredential(t, "a", "openai"), in.addCredential(t, "b", "openai")
	got, err := in.svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Credentials) != 2 {
		t.Errorf("RemoveUpstream reports %+v", got.Credentials)
	}
	for _, c := range []admin.Credential{a, b} {
		if _, ok := in.vault.Credential(c.ID); ok {
			t.Errorf("the vault still holds %s", c.Name)
		}
		if _, ok := in.reg.Credential(c.ID); ok {
			t.Errorf("the registry still holds %s", c.Name)
		}
	}
	var got2 []string
	for _, e := range in.latest(t, 3) {
		got2 = append(got2, e.Action)
	}
	if want := []string{"credential.delete", "credential.delete", "upstream.delete"}; !slices.Equal(got2, want) {
		t.Errorf("audit = %v, want %v", got2, want)
	}
	// It was one transaction, so one change.
	last := in.applied[len(in.applied)-1]
	if len(last.DeletedCredentials) != 2 || len(last.DeletedUpstreams) != 1 {
		t.Errorf("published %+v", last)
	}
	// Their names are free again.
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai"}); err != nil {
		t.Fatal(err)
	}
	in.addCredential(t, "a", "openai")
}

func TestAdminTokenCommandsMoveTheRegistrysGeneration(t *testing.T) {
	in := newInstall(t)
	before := in.reg.Generation()
	if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	if g := in.reg.Generation(); g <= before {
		t.Errorf("the generation went from %d to %d", before, g)
	}
}

func TestLoadRebuildsMemoryFromTheStore(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	gone := in.addCredential(t, "gone", "openai")
	if _, err := in.svc.RemoveCredential(as("owner"), admin.CredentialRef{Ref: "gone"}); err != nil {
		t.Fatal(err)
	}
	u, err := in.svc.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	generation := in.reg.Generation()

	// A restart: an empty registry and vault over the same store.
	reg := registry.New()
	in.vault.Close()
	v := unlockVault(t, in.store)
	svc := admin.New(admin.Options{Store: in.store, Now: func() time.Time { return in.now }, Vault: v, Presets: nil, Apply: reg.Apply})
	if err := svc.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if g := reg.Generation(); g != generation {
		t.Errorf("generation = %d, want %d", g, generation)
	}
	if ru, ok := reg.Upstream(u.ID); !ok || ru.Host != "api.openai.com" {
		t.Errorf("upstream = %+v, %v", ru, ok)
	}
	if rc, ok := reg.Credential(c.ID); !ok || rc.Name != "main" {
		t.Errorf("credential = %+v, %v", rc, ok)
	}
	if plain, ok := v.Credential(c.ID); !ok || string(plain) != secret {
		t.Errorf("the vault holds %q, %v", plain, ok)
	}
	if _, ok := reg.Credential(gone.ID); ok {
		t.Error("the deleted credential came back")
	}
	if _, ok := v.Credential(gone.ID); ok {
		t.Error("the deleted credential's secret came back")
	}
}

func TestLoadRefusesACredentialThatDoesNotDecrypt(t *testing.T) {
	in := withOpenAI(t)
	c := in.addCredential(t, "main", "openai")
	in.sqlExec(t, `UPDATE credentials SET ciphertext = x'00112233' WHERE id = ?`, c.ID)
	err := in.svc.Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), c.ID) {
		t.Fatalf("Load = %v, want an error naming %s", err, c.ID)
	}
}

func TestPublishesKeepCommitOrderUnderConcurrentCommands(t *testing.T) {
	in := withOpenAI(t)
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			host := fmt.Sprintf("h%d.example.com", i)
			if _, err := in.svc.UpdateUpstream(as("owner"), admin.UpstreamUpdate{Ref: "openai", UpstreamSettings: admin.UpstreamSettings{Host: &host}}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for i := 1; i < len(in.applied); i++ {
		if in.applied[i].Generation <= in.applied[i-1].Generation {
			t.Errorf("published generation %d after %d", in.applied[i].Generation, in.applied[i-1].Generation)
		}
	}
	u, err := in.svc.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if ru, _ := in.reg.Upstream(u.ID); ru.Host != u.Host {
		t.Errorf("the registry holds %s, the store %s", ru.Host, u.Host)
	}
}

func TestASecretPrintsAsRedacted(t *testing.T) {
	s := admin.Secret(secret)
	req := admin.AddCredentialRequest{Name: "x", Upstream: "y", Secret: s}
	for _, printed := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s %q", s, s, s, s, s), fmt.Sprintf("%v %+v %#v", req, req, req)} {
		if strings.Contains(printed, secret) {
			t.Errorf("printed %q", printed)
		}
	}
}

func TestAnUpstreamCanBeAddedWithItsCredentialInOneCommand(t *testing.T) {
	in := newInstall(t)
	generation := in.reg.Generation()
	u, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai",
		Credential: &admin.NewCredential{Secret: secret}})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Credentials) != 1 || u.Credentials[0].Name != "openai" || u.Credentials[0].Last4 != "WXYZ" {
		t.Fatalf("credentials = %+v, want one named for the upstream", u.Credentials)
	}
	id := u.Credentials[0].ID
	if plain, ok := in.vault.Credential(id); !ok || string(plain) != secret {
		t.Errorf("the vault holds %q, %v", plain, ok)
	}
	if rc, ok := in.reg.Credential(id); !ok || rc.UpstreamID != u.ID {
		t.Errorf("the registry holds %+v, %v", rc, ok)
	}
	// One transaction, so one generation and one publish.
	if g := in.reg.Generation(); g != generation+1 {
		t.Errorf("generation went from %d to %d", generation, g)
	}
	var actions []string
	for _, e := range in.latest(t, 2) {
		actions = append(actions, e.Action)
	}
	if want := []string{"upstream.create", "credential.create"}; !slices.Equal(actions, want) {
		t.Errorf("audit = %v, want %v", actions, want)
	}
}

func TestANamedCredentialCanBeAddedWithItsUpstream(t *testing.T) {
	in := newInstall(t)
	u, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai",
		Credential: &admin.NewCredential{Name: "main", Secret: secret}})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Credentials) != 1 || u.Credentials[0].Name != "main" {
		t.Errorf("credentials = %+v", u.Credentials)
	}
}

// A bad secret must not strand an upstream with nothing to authenticate it:
// the operator fixes the secret and runs the same command again.
func TestABadCredentialLeavesNoUpstreamBehind(t *testing.T) {
	in := newInstall(t)
	before := len(in.latest(t, 100))
	generation := in.reg.Generation()
	_, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "twilio", Preset: "twilio",
		Credential: &admin.NewCredential{Secret: "no-colon-here"}})
	if code(err) != admin.CodeInvalid {
		t.Fatalf("AddUpstream = %v, want invalid", err)
	}
	if _, err := in.svc.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "twilio"}); code(err) != admin.CodeNotFound {
		t.Errorf("the upstream was left behind: %v", err)
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("the audit log grew by %d", after-before)
	}
	if g := in.reg.Generation(); g != generation {
		t.Errorf("the registry moved to generation %d", g)
	}
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "twilio", Preset: "twilio",
		Credential: &admin.NewCredential{Secret: "user:password"}}); err != nil {
		t.Errorf("running it again: %v", err)
	}
}
