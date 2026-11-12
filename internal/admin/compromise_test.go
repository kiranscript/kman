package admin_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/profiles"
)

const replacement = "sk-live-replacement-0123456789abcdefQRST"

// compromised is an install whose credential main has three keys not revoked
// (my-app, other-app, and paused, which is disabled) and a revoked one, and
// whose credential spare has one key.
type compromised struct {
	in                                  *install
	main                                admin.Credential
	myApp, otherApp, paused, gone, safe admin.CreatedKey
}

func withCompromisableCredential(t *testing.T) compromised {
	t.Helper()
	in := withOpenAI(t)
	c := compromised{in: in, main: in.addCredential(t, "main", "openai")}
	in.addCredential(t, "spare", "openai")
	make := func(name, credential string) admin.CreatedKey {
		in.now = in.now.Add(time.Minute) // oldest first has a clear order
		return in.createKey(t, admin.CreateKeyRequest{Name: name, Upstream: "openai", Credential: credential})
	}
	c.myApp, c.otherApp, c.paused = make("my-app", "main"), make("other-app", "main"), make("paused", "main")
	c.gone, c.safe = make("gone", "main"), make("safe", "spare")
	if _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: c.paused.Key.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: c.gone.Key.ID}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestACompromisedCredentialIsReplacedAndEveryKeyOnItRotatedAtOnce(t *testing.T) {
	c := withCompromisableCredential(t)
	in := c.in
	in.now = in.now.Add(time.Hour)

	got, err := in.svc.CompromiseCredential(as("owner"), admin.CompromiseCredentialRequest{Ref: "main", Secret: replacement})
	if err != nil {
		t.Fatal(err)
	}

	if got.Credential.ID != c.main.ID || got.Credential.Last4 != "QRST" || got.Credential.RotatedAt == nil || !got.Credential.RotatedAt.Equal(in.now) {
		t.Errorf("credential = %+v", got.Credential)
	}
	if plain, _ := in.vault.Credential(c.main.ID); string(plain) != replacement {
		t.Errorf("the vault holds %q, want the replacement", plain)
	}
	var names []string
	for _, r := range got.RotatedKeys {
		names = append(names, r.Key.Name)
		if r.PreviousValidUntil != nil || r.Secret == "" {
			t.Errorf("rotated key %s = %+v: a compromise ends the old secret at once and returns a new one", r.Key.Name, r)
		}
	}
	if want := "my-app,other-app,paused"; strings.Join(names, ",") != want {
		t.Errorf("rotated keys = %v, want %s: every key on the credential that is not revoked, oldest first", names, want)
	}
	for _, k := range []admin.CreatedKey{c.myApp, c.otherApp, c.paused} {
		if in.works(t, k.Key.ID, k.Secret) {
			t.Errorf("the old secret of %s still works", k.Key.Name)
		}
	}
	for i, r := range got.RotatedKeys {
		if !in.works(t, r.Key.ID, r.Secret) {
			t.Errorf("the new secret of %s does not work", r.Key.Name)
		}
		if i == 2 && r.Key.Status != "disabled" {
			t.Errorf("paused is %q after the compromise, want it still disabled", r.Key.Status)
		}
	}
	for _, k := range []admin.CreatedKey{c.safe, c.gone} {
		if !in.works(t, k.Key.ID, k.Secret) {
			t.Errorf("%s, not on the credential or revoked, was rotated", k.Key.Name)
		}
	}
	if got.RotatedKeys[0].SDKEnv != "OPENAI_API_KEY" {
		t.Errorf("sdk env = %q", got.RotatedKeys[0].SDKEnv)
	}
}

func TestACompromiseIsAuditedInOneEntryEachAndHoldsNoSecret(t *testing.T) {
	c := withCompromisableCredential(t)
	in := c.in
	got, err := in.svc.CompromiseCredential(as("owner"), admin.CompromiseCredentialRequest{Ref: "main", Secret: replacement})
	if err != nil {
		t.Fatal(err)
	}
	entries := in.latest(t, 4)
	want := []string{
		"owner credential.compromise credential/" + c.main.ID,
		"owner key.rotate key/" + c.myApp.Key.ID,
		"owner key.rotate key/" + c.otherApp.Key.ID,
		"owner key.rotate key/" + c.paused.Key.ID,
	}
	for i, e := range entries {
		if summary(e) != want[i] {
			t.Errorf("audit[%d] = %q, want %q", i, summary(e), want[i])
		}
		blob := string(e.Before) + string(e.After)
		if strings.Contains(blob, replacement) || strings.Contains(blob, secret) {
			t.Errorf("the entry holds the credential: %s", blob)
		}
		for _, r := range got.RotatedKeys {
			if strings.Contains(blob, strings.Split(string(r.Secret), "_")[2]) {
				t.Errorf("the entry holds a key's secret: %s", blob)
			}
		}
	}
}

func TestACompromiseThatFailsHalfwayChangesNothing(t *testing.T) {
	c := withCompromisableCredential(t)
	in := c.in
	// Enough randomness for the first key's new secret and none for the second.
	in.rand = &failingReader{allow: 32}
	in.svc = in.service(profiles.Presets())
	generation, audits := in.reg.Generation(), len(in.latest(t, 100))
	oldPlain, _ := in.vault.Credential(c.main.ID)
	oldPlain = append([]byte(nil), oldPlain...)

	_, err := in.svc.CompromiseCredential(as("owner"), admin.CompromiseCredentialRequest{Ref: "main", Secret: replacement})
	if err == nil {
		t.Fatal("the compromise succeeded")
	}

	if plain, _ := in.vault.Credential(c.main.ID); string(plain) != string(oldPlain) {
		t.Errorf("the vault holds %q, want the credential as it was", plain)
	}
	for _, k := range []admin.CreatedKey{c.myApp, c.otherApp, c.paused} {
		if !in.works(t, k.Key.ID, k.Secret) {
			t.Errorf("%s was rotated although the compromise failed", k.Key.Name)
		}
		if s := in.states(t, k.Key.ID); len(s) != 1 {
			t.Errorf("%s holds %v", k.Key.Name, s)
		}
	}
	if in.reg.Generation() != generation || len(in.latest(t, 100)) != audits {
		t.Error("a failed compromise was published or audited")
	}
	got, _ := in.svc.ListUpstreams(as("owner"))
	for _, u := range got.Upstreams {
		for _, cr := range u.Credentials {
			if cr.ID == c.main.ID && cr.RotatedAt != nil {
				t.Errorf("the credential shows rotated at %v", cr.RotatedAt)
			}
		}
	}
}

type failingReader struct{ allow int }

func (f *failingReader) Read(p []byte) (int, error) {
	if len(p) > f.allow {
		return 0, errors.New("no more randomness")
	}
	f.allow -= len(p)
	return constantReader(1).Read(p)
}

func TestCompromisingACredentialWithNoKeysJustReplacesIt(t *testing.T) {
	in := withOpenAI(t)
	cred := in.addCredential(t, "main", "openai")
	got, err := in.svc.CompromiseCredential(as("owner"), admin.CompromiseCredentialRequest{Ref: "main", Secret: replacement})
	if err != nil {
		t.Fatal(err)
	}
	if got.Credential.ID != cred.ID || len(got.RotatedKeys) != 0 {
		t.Errorf("got %+v", got)
	}
	if plain, _ := in.vault.Credential(cred.ID); string(plain) != replacement {
		t.Errorf("the vault holds %q", plain)
	}
}

func TestCompromiseRefusesABadSecretAndAMissingCredential(t *testing.T) {
	c := withCompromisableCredential(t)
	in := c.in
	for _, tc := range []struct {
		req  admin.CompromiseCredentialRequest
		want admin.Code
	}{
		{admin.CompromiseCredentialRequest{Ref: "main", Secret: ""}, admin.CodeInvalid},
		{admin.CompromiseCredentialRequest{Ref: "main", Secret: "two\nlines"}, admin.CodeInvalid},
		{admin.CompromiseCredentialRequest{Ref: "nope", Secret: replacement}, admin.CodeNotFound},
	} {
		if _, err := in.svc.CompromiseCredential(as("owner"), tc.req); code(err) != tc.want {
			t.Errorf("CompromiseCredential(%q, %q) = %v, want code %q", tc.req.Ref, "…", err, tc.want)
		}
	}
	if !in.works(t, c.myApp.Key.ID, c.myApp.Secret) {
		t.Error("a refused compromise rotated a key")
	}
}
