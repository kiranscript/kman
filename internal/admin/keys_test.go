package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

const days = 24 * time.Hour

func seconds(d time.Duration) *int64 {
	n := int64(d / time.Second)
	return &n
}

// withCredential is an install with an openai upstream and one credential.
func withCredential(t *testing.T) (*install, admin.Credential) {
	t.Helper()
	in := withOpenAI(t)
	return in, in.addCredential(t, "main", "openai")
}

func (in *install) createKey(t *testing.T, req admin.CreateKeyRequest) admin.CreatedKey {
	t.Helper()
	created, err := in.svc.CreateKey(as("owner"), req)
	if err != nil {
		t.Fatalf("CreateKey(%+v) = %v", req, err)
	}
	return created
}

func TestANewKeyExpiresAfterTheDefaultTTLWithAnEmptyPolicy(t *testing.T) {
	in, c := withCredential(t)
	created := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
	k := created.Key
	if !k.ExpiresAt.Equal(t0.Add(90 * days)) {
		t.Errorf("expires at %v, want 90 days from now", k.ExpiresAt)
	}
	if string(k.Policy) != `{}` || k.PolicyVersion != 1 {
		t.Errorf("policy = %s at version %d, want {} at 1", k.Policy, k.PolicyVersion)
	}
	if k.Name != "my-app" || k.Upstream != "openai" || k.Credential != "main" || k.CredentialID != c.ID ||
		k.Status != "active" || k.Expired || !k.CreatedAt.Equal(t0) {
		t.Errorf("key = %+v", k)
	}
	if created.SDKEnv != "OPENAI_API_KEY" {
		t.Errorf("sdk env = %q", created.SDKEnv)
	}
}

func TestTheSecretIsAVirtualKeyAndOnlyItsHashIsStored(t *testing.T) {
	in, _ := withCredential(t)
	created := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"})
	tok, err := token.Parse(string(created.Secret))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Kind != token.VirtualKey || string(tok.ID) != created.Key.ID {
		t.Errorf("the secret parses as %+v, for key %s", tok, created.Key.ID)
	}
	err = in.store.Do(context.Background(), func(tx *store.Tx) error {
		secrets, err := tx.KeySecrets(created.Key.ID)
		if err != nil {
			return err
		}
		if len(secrets) != 1 || !bytes.Equal(secrets[0].Hash, tok.Hash[:]) || secrets[0].State != store.SecretCurrent {
			t.Errorf("stored secrets = %+v, want the one hash %x", secrets, tok.Hash)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreatingAKeyIsAuditedAndPublished(t *testing.T) {
	in, c := withCredential(t)
	before := in.reg.Generation()
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	entries := in.latest(t, 1)
	if got, want := summary(entries[0]), "owner key.create key/"+k.ID; got != want {
		t.Errorf("audit = %q, want %q", got, want)
	}
	if !strings.Contains(string(entries[0].After), `"my-app"`) {
		t.Errorf("audit after = %s", entries[0].After)
	}
	if in.reg.Generation() <= before {
		t.Errorf("generation stayed at %d", before)
	}
	route, ok := in.reg.Lookup(k.ID)
	if !ok {
		t.Fatal("the registry has no route for the new key")
	}
	if route.Key.Name != "my-app" || route.Key.CredentialID != c.ID || route.Key.Status != "active" ||
		!route.Key.ExpiresAt.Equal(k.ExpiresAt) || route.Key.Policy != `{}` || route.Key.PolicyVersion != 1 {
		t.Errorf("registry key = %+v", *route.Key)
	}
	if route.Credential == nil || route.Credential.ID != c.ID || route.Upstream == nil || route.Upstream.Name != "openai" {
		t.Errorf("route = %+v", route)
	}
}

func TestLoadPutsEveryKeyInTheRegistry(t *testing.T) {
	in, c := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key

	in.reg = registry.New()
	if err := in.service(nil).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	route, ok := in.reg.Lookup(k.ID)
	if !ok {
		t.Fatal("no route after Load")
	}
	if route.Key.Name != "my-app" || route.Credential == nil || route.Credential.ID != c.ID || !route.Key.ExpiresAt.Equal(k.ExpiresAt) {
		t.Errorf("route = %+v, key %+v", route, *route.Key)
	}
}

func TestATTLMayNotPassTheMaximumKeyLifetime(t *testing.T) {
	in, _ := withCredential(t)
	in.createKey(t, admin.CreateKeyRequest{Name: "at-max", Upstream: "openai", TTLSeconds: seconds(365 * days)})
	before := len(in.latest(t, 100))
	for _, ttl := range []*int64{seconds(365*days + time.Second), seconds(0), seconds(-time.Hour)} {
		_, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "openai", TTLSeconds: ttl})
		if code(err) != admin.CodeInvalid {
			t.Errorf("CreateKey with a TTL of %ds = %v, want invalid", *ttl, err)
		}
	}
	// What would overflow a time.Duration is refused too.
	huge := int64(1) << 62
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "openai", TTLSeconds: &huge}); code(err) != admin.CodeInvalid {
		t.Errorf("CreateKey with a huge TTL = %v, want invalid", err)
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("the refusals wrote %d audit entries", after-before)
	}
}

func TestANewKeyFollowsTheSettings(t *testing.T) {
	in, _ := withCredential(t)
	ttl, max := int64(7*24*60*60), int64(30*24*60*60)
	if _, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{DefaultKeyTTLSeconds: &ttl, MaxKeyLifetimeSeconds: &max}); err != nil {
		t.Fatal(err)
	}
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key
	if !k.ExpiresAt.Equal(t0.Add(7 * days)) {
		t.Errorf("expires at %v, want 7 days from now", k.ExpiresAt)
	}
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "openai", TTLSeconds: seconds(31 * days)}); code(err) != admin.CodeInvalid {
		t.Errorf("31 days past a 30-day maximum = %v, want invalid", err)
	}
}

func TestANameIsOptionalAndUniqueAmongKeysNotRevoked(t *testing.T) {
	in, _ := withCredential(t)
	a := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key
	b := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key
	if a.Name != "" || b.Name != "" || a.ID == b.ID {
		t.Errorf("unnamed keys = %+v, %+v", a, b)
	}
	in.createKey(t, admin.CreateKeyRequest{Name: "app", Upstream: "openai"})
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Name: "app", Upstream: "openai"}); code(err) != admin.CodeConflict {
		t.Errorf("a second key named app = %v, want conflict", err)
	}
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Name: "Not Valid", Upstream: "openai"}); code(err) != admin.CodeInvalid {
		t.Errorf("a bad name = %v, want invalid", err)
	}
}

func TestAKeyTakesItsUpstreamsOnlyCredentialOrTheOneNamed(t *testing.T) {
	in := withOpenAI(t)
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "openai"}); code(err) != admin.CodeConflict ||
		!strings.Contains(err.Error(), "kman credential add") {
		t.Errorf("no credential = %v, want a conflict naming the command", err)
	}
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "nothere"}); code(err) != admin.CodeNotFound {
		t.Errorf("no upstream = %v, want not found", err)
	}
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{}); code(err) != admin.CodeInvalid {
		t.Errorf("naming no upstream = %v, want invalid", err)
	}
	first := in.addCredential(t, "first", "openai")
	if k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key; k.CredentialID != first.ID {
		t.Errorf("credential = %s, want the only one", k.CredentialID)
	}
	second := in.addCredential(t, "second", "openai")
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "openai"}); code(err) != admin.CodeInvalid ||
		!strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Errorf("two credentials = %v, want invalid naming both", err)
	}
	for _, ref := range []string{"second", second.ID} {
		if k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai", Credential: ref}).Key; k.CredentialID != second.ID {
			t.Errorf("credential %s = %s", ref, k.CredentialID)
		}
	}

	// A credential on another upstream is refused.
	paths := []string{"* /**"}
	host := "other.example.test"
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "other",
		UpstreamSettings: admin.UpstreamSettings{Host: &host, Paths: &paths}}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "other", Credential: "first"}); code(err) != admin.CodeInvalid {
		t.Errorf("another upstream's credential = %v, want invalid", err)
	}
}

func TestNoteAndTagsAreCheckedAndKept(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai", Note: "billing job, ünïcode", Tags: []string{"prod", "team:ml"}}).Key
	if k.Note != "billing job, ünïcode" || strings.Join(k.Tags, ",") != "prod,team:ml" {
		t.Errorf("key = %+v", k)
	}
	if u := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key; u.Tags == nil || len(u.Tags) != 0 {
		t.Errorf("no tags = %#v, want an empty list", u.Tags)
	}
	for name, req := range map[string]admin.CreateKeyRequest{
		"a control character": {Note: "line\nbreak"},
		"a long note":         {Note: strings.Repeat("x", 2000)},
		"a bad tag":           {Tags: []string{"Has Space"}},
		"a repeated tag":      {Tags: []string{"a", "a"}},
		"too many tags":       {Tags: manyTags(40)},
	} {
		req.Upstream = "openai"
		if _, err := in.svc.CreateKey(as("owner"), req); code(err) != admin.CodeInvalid {
			t.Errorf("%s = %v, want invalid", name, err)
		}
	}
}

func manyTags(n int) []string {
	var out []string
	for i := range n {
		out = append(out, fmt.Sprintf("tag-%d", i))
	}
	return out
}

func TestAKeyMayBeCreatedWithAPolicyThatPolicyAccepts(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai", Policy: json.RawMessage(` { } `)}).Key
	if string(k.Policy) != `{}` {
		t.Errorf("policy = %s, want it normalized", k.Policy)
	}
	for _, raw := range []string{`null`, `[]`, `{"no_such_field":1}`} {
		_, err := in.svc.CreateKey(as("owner"), admin.CreateKeyRequest{Upstream: "openai", Policy: json.RawMessage(raw)})
		if code(err) != admin.CodeInvalid {
			t.Errorf("policy %s = %v, want invalid", raw, err)
		}
	}
}

func TestKeysAreReadByIdAndListedByName(t *testing.T) {
	in, _ := withCredential(t)
	a := in.createKey(t, admin.CreateKeyRequest{Name: "app-a", Upstream: "openai"}).Key
	in.now = t0.Add(time.Minute)
	b := in.createKey(t, admin.CreateKeyRequest{Name: "app-b", Upstream: "openai"}).Key
	got, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: b.ID})
	if err != nil || got.ID != b.ID || got.Upstream != "openai" {
		t.Errorf("GetKey = %+v, %v", got, err)
	}
	// The API uses key ids: a name is no way to address a key.
	if _, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: "app-a"}); code(err) != admin.CodeNotFound {
		t.Errorf("GetKey by name = %v, want not found", err)
	}
	all, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{})
	if err != nil || len(all.Keys) != 2 || all.Keys[0].ID != a.ID || all.Keys[1].ID != b.ID {
		t.Errorf("ListKeys = %+v, %v", all, err)
	}
	named, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{Name: "app-b"})
	if err != nil || len(named.Keys) != 1 || named.Keys[0].ID != b.ID {
		t.Errorf("ListKeys by name = %+v, %v", named, err)
	}
	none, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{Name: "nothere"})
	if err != nil || none.Keys == nil || len(none.Keys) != 0 {
		t.Errorf("ListKeys by a name no key has = %#v, %v", none, err)
	}
}

func TestAKeyPastItsExpiryIsShownAsExpired(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai", TTLSeconds: seconds(time.Hour)}).Key
	in.now = t0.Add(time.Hour)
	got, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil || !got.Expired || got.Status != "active" {
		t.Errorf("at its expiry, GetKey = %+v, %v", got, err)
	}
}

func TestAKeyCommandNeedsACaller(t *testing.T) {
	in, _ := withCredential(t)
	if _, err := in.svc.CreateKey(context.Background(), admin.CreateKeyRequest{Upstream: "openai"}); code(err) != admin.CodeUnauthorized {
		t.Errorf("CreateKey = %v", err)
	}
	if _, err := in.svc.ListKeys(context.Background(), admin.KeyQuery{}); code(err) != admin.CodeUnauthorized {
		t.Errorf("ListKeys = %v", err)
	}
}

// The secret is in the create response and nowhere else: not on any other
// response, the audit log, the registry, or anywhere in the database's files.
func TestTheSecretAppearsOnlyInTheCreateResponse(t *testing.T) {
	in, _ := withCredential(t)
	created := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
	secret := string(created.Secret)
	body := secret[strings.LastIndex(secret, "_")+1:] // the secret without its prefix and key id
	note := "changed"

	check := func(what string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw) + fmt.Sprintf("%+v", v)
		if strings.Contains(text, secret) || strings.Contains(text, body[:32]) {
			t.Errorf("%s holds the secret: %s", what, text)
		}
	}
	check("the created key's %v", created.Key)
	got, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: created.Key.ID})
	check("GetKey", got)
	if err != nil {
		t.Fatal(err)
	}
	list, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{})
	check("ListKeys", list)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: created.Key.ID, Note: &note})
	check("UpdateKey", updated)
	if err != nil {
		t.Fatal(err)
	}
	check("the audit log", in.latest(t, 100))
	route, _ := in.reg.Lookup(created.Key.ID)
	check("the registry", *route.Key)

	// A printed create response shows a placeholder: only JSON sees the secret.
	if text := fmt.Sprintf("%v %+v %s", created, created, created.Secret); strings.Contains(text, secret) {
		t.Errorf("printing the response shows the secret: %s", text)
	}

	files, err := filepath.Glob(filepath.Join(in.dir, store.FileName+"*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("the database's files: %v, %v", files, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(body[:32])) {
			t.Errorf("%s holds the secret", f)
		}
	}
}

func TestUpdatingAKeyChangesWhatItNamesAndIsAudited(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai", Note: "old", Tags: []string{"a"}}).Key
	name, note, tags := "renamed", "new", []string{"b", "c"}
	gen := in.reg.Generation()

	got, err := in.svc.UpdateKey(as("ci"), admin.KeyUpdate{ID: k.ID, Name: &name, Note: &note, Tags: &tags})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" || got.Note != "new" || strings.Join(got.Tags, ",") != "b,c" || !got.ExpiresAt.Equal(k.ExpiresAt) {
		t.Errorf("UpdateKey = %+v", got)
	}
	entry := in.latest(t, 1)[0]
	if s := summary(entry); s != "ci key.update key/"+k.ID {
		t.Errorf("audit = %q", s)
	}
	if !strings.Contains(string(entry.Before), `"my-app"`) || !strings.Contains(string(entry.After), `"renamed"`) {
		t.Errorf("audit before %s, after %s", entry.Before, entry.After)
	}
	route, _ := in.reg.Lookup(k.ID)
	if route.Key.Name != "renamed" || in.reg.Generation() <= gen {
		t.Errorf("registry key = %+v at generation %d (was %d)", *route.Key, in.reg.Generation(), gen)
	}
}

func TestAnUpdateCanClearANameANoteAndTags(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai", Note: "old", Tags: []string{"a"}}).Key
	empty, none := "", []string{}
	got, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, Name: &empty, Note: &empty, Tags: &none})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "" || got.Note != "" || len(got.Tags) != 0 {
		t.Errorf("UpdateKey = %+v", got)
	}
}

func TestAnUpdateThatChangesNothingRecordsNothing(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai", Note: "same"}).Key
	before, gen := len(in.latest(t, 100)), in.reg.Generation()
	name, note := "my-app", "same"
	got, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, Name: &name, Note: &note, Policy: json.RawMessage(`{}`)})
	if err != nil || got.Name != "my-app" {
		t.Errorf("UpdateKey = %+v, %v", got, err)
	}
	if after := len(in.latest(t, 100)); after != before || in.reg.Generation() != gen {
		t.Errorf("the audit log went from %d to %d, the generation from %d to %d", before, after, gen, in.reg.Generation())
	}
	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID}); code(err) != admin.CodeInvalid {
		t.Errorf("an update naming nothing = %v, want invalid", err)
	}
}

func TestAnUpdateIsCheckedLikeACreate(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	in.createKey(t, admin.CreateKeyRequest{Name: "taken", Upstream: "openai"})
	bad, taken, ctrl, tags := "Not Valid", "taken", "a\x1b[31m", []string{"Has Space"}
	for what, req := range map[string]struct {
		update admin.KeyUpdate
		want   admin.Code
	}{
		"a bad name":          {admin.KeyUpdate{Name: &bad}, admin.CodeInvalid},
		"a taken name":        {admin.KeyUpdate{Name: &taken}, admin.CodeConflict},
		"a control character": {admin.KeyUpdate{Note: &ctrl}, admin.CodeInvalid},
		"a bad tag":           {admin.KeyUpdate{Tags: &tags}, admin.CodeInvalid},
		"a zero TTL":          {admin.KeyUpdate{TTLSeconds: seconds(0)}, admin.CodeInvalid},
		"a bad policy":        {admin.KeyUpdate{Policy: json.RawMessage(`{"no_such_field":1}`)}, admin.CodeInvalid},
		"a null policy":       {admin.KeyUpdate{Policy: json.RawMessage(`null`)}, admin.CodeInvalid},
	} {
		req.update.ID = k.ID
		if _, err := in.svc.UpdateKey(as("owner"), req.update); code(err) != req.want {
			t.Errorf("%s = %v, want %s", what, err, req.want)
		}
	}
	note := "x"
	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: "my-app", Note: &note}); code(err) != admin.CodeNotFound {
		t.Errorf("UpdateKey by name = %v, want not found", err)
	}
}

func TestAKeysPolicyIsEditedThroughUpdateAndAudited(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key

	// The real policy has no fields yet, so these rules stand for a later one.
	in.normalize = func(raw []byte) (int, []byte, error) {
		var b bytes.Buffer
		err := json.Compact(&b, raw)
		return 2, b.Bytes(), err
	}
	got, err := in.svc.UpdateKey(as("ci"), admin.KeyUpdate{ID: k.ID, Policy: json.RawMessage(`{ "rpm": 5 }`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Policy) != `{"rpm":5}` || got.PolicyVersion != 2 {
		t.Errorf("policy = %s at version %d", got.Policy, got.PolicyVersion)
	}
	entry := in.latest(t, 1)[0]
	if s := summary(entry); s != "ci key.update key/"+k.ID {
		t.Errorf("audit = %q", s)
	}
	if !strings.Contains(string(entry.Before), `"policy":{}`) || !strings.Contains(string(entry.After), `"policy":{"rpm":5}`) {
		t.Errorf("audit before %s, after %s", entry.Before, entry.After)
	}
	route, _ := in.reg.Lookup(k.ID)
	if route.Key.Policy != `{"rpm":5}` || route.Key.PolicyVersion != 2 {
		t.Errorf("registry key = %+v", *route.Key)
	}
	shown, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: k.ID})
	if err != nil || string(shown.Policy) != `{"rpm":5}` {
		t.Errorf("GetKey = %s, %v", shown.Policy, err)
	}
}

func TestAKeyMayNotBeExtendedPastTheMaximumLifetimeFromItsCreation(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"}).Key
	in.now = t0.Add(300 * days)

	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, TTLSeconds: seconds(66 * days)}); code(err) != admin.CodeInvalid ||
		!strings.Contains(err.Error(), "2027-11-04") {
		t.Errorf("an extension past 365 days from creation = %v, want invalid naming the limit", err)
	}
	got, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, TTLSeconds: seconds(65 * days)})
	if err != nil {
		t.Fatal(err)
	}
	if want := t0.Add(365 * days); !got.ExpiresAt.Equal(want) {
		t.Errorf("expires at %v, want %v", got.ExpiresAt, want)
	}
	if route, _ := in.reg.Lookup(k.ID); !route.Key.ExpiresAt.Equal(got.ExpiresAt) {
		t.Errorf("registry key expires at %v", route.Key.ExpiresAt)
	}
}

// Lowering the maximum leaves a key past the new limit, and it must still be
// possible to bring that key closer to it.
func TestAKeyPastALoweredMaximumMayStillBeShortened(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai", TTLSeconds: seconds(300 * days)}).Key
	ttl, max := int64(day), int64(30*day)
	if _, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{DefaultKeyTTLSeconds: &ttl, MaxKeyLifetimeSeconds: &max}); err != nil {
		t.Fatal(err)
	}
	got, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, TTLSeconds: seconds(100 * days)})
	if err != nil {
		t.Fatalf("shortening = %v", err)
	}
	if !got.ExpiresAt.Equal(t0.Add(100 * days)) {
		t.Errorf("expires at %v", got.ExpiresAt)
	}
	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, TTLSeconds: seconds(101 * days)}); code(err) != admin.CodeInvalid {
		t.Errorf("extending again = %v, want invalid", err)
	}
}

func TestARevokedKeyCannotBeUpdated(t *testing.T) {
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	in.setKeyStatus(t, k.ID, "revoked")
	note := "x"
	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: k.ID, Note: &note}); code(err) != admin.CodeConflict {
		t.Errorf("UpdateKey = %v, want conflict", err)
	}
	list, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{Name: "my-app"})
	if err != nil || len(list.Keys) != 0 {
		t.Errorf("a revoked key is found by its name: %+v, %v", list, err)
	}
}

// secretsIn is what the registry holds to authenticate a key.
func secretsIn(t *testing.T, in *install, keyID string) []registry.KeySecret {
	t.Helper()
	route, ok := in.reg.Lookup(keyID)
	if !ok {
		t.Fatalf("the registry has no key %s", keyID)
	}
	return route.Key.Secrets
}

// The request path authenticates from the snapshot, with no store read, so the
// hash must be there after everything that publishes a key.
func TestTheRegistryHoldsTheHashOfAKeysSecret(t *testing.T) {
	in, _ := withCredential(t)
	created := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
	tok, err := token.Parse(string(created.Secret))
	if err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		got := secretsIn(t, in, created.Key.ID)
		if len(got) != 1 || !got[0].Hash.Equal(tok.Hash) || !got[0].ValidUntil.IsZero() {
			t.Errorf("%s, the registry holds %+v", when, got)
		}
	}
	check("after create")

	note := "moved"
	if _, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: created.Key.ID, Note: &note}); err != nil {
		t.Fatal(err)
	}
	check("after update")

	in.reg = registry.New()
	if err := in.service(nil).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("after a restart")
}

// A previous secret works until its grace ends, and a retired one never
// authenticates, so it never reaches the request path.
func TestTheRegistryHoldsOnlySecretsThatCanAuthenticate(t *testing.T) {
	in, _ := withCredential(t)
	created := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"})
	previous := bytes.Repeat([]byte{1}, 32)
	retired := bytes.Repeat([]byte{2}, 32)
	graceEnds := t0.Add(time.Hour)
	in.sqlExec(t, `INSERT INTO key_secrets (key_id, secret_hash, state, created_at, valid_until) VALUES (?, ?, 'previous', ?, ?)`,
		created.Key.ID, previous, t0.UnixMilli(), graceEnds.UnixMilli())
	in.sqlExec(t, `INSERT INTO key_secrets (key_id, secret_hash, state, created_at) VALUES (?, ?, 'retired', ?)`,
		created.Key.ID, retired, t0.UnixMilli())

	in.reg = registry.New()
	if err := in.service(nil).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := secretsIn(t, in, created.Key.ID)
	if len(got) != 2 {
		t.Fatalf("the registry holds %d secrets, want the current and the previous: %+v", len(got), got)
	}
	for _, s := range got {
		if bytes.Equal(s.Hash[:], retired) {
			t.Error("a retired secret reached the registry")
		}
		if bytes.Equal(s.Hash[:], previous) && !s.ValidUntil.Equal(graceEnds) {
			t.Errorf("the previous secret is valid until %v, want %v", s.ValidUntil, graceEnds)
		}
	}
}

func TestLoadRefusesASecretHashOfTheWrongLength(t *testing.T) {
	in, _ := withCredential(t)
	created := in.createKey(t, admin.CreateKeyRequest{Upstream: "openai"})
	in.sqlExec(t, `UPDATE key_secrets SET secret_hash = x'0011' WHERE key_id = ?`, created.Key.ID)
	err := in.service(nil).Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), created.Key.ID) {
		t.Errorf("Load = %v, want an error naming the key", err)
	}
}
