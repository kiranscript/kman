package admin_test

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

func (in *install) createCanary(t *testing.T, req admin.CreateCanaryRequest) admin.CreatedCanary {
	t.Helper()
	created, err := in.svc.CreateCanary(as("owner"), req)
	if err != nil {
		t.Fatalf("CreateCanary(%+v) = %v", req, err)
	}
	return created
}

// fire records a use of the canary, as the priority lane does.
func (in *install) fire(t *testing.T, id string, at time.Time, use store.CanaryUse) {
	t.Helper()
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		_, _, err := tx.RecordCanaryUse(id, at, use)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestACanaryIsAKeyInTheStandardFormatWithNoCredentialAndNoExpiry(t *testing.T) {
	in := newInstall(t)
	created := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "the deploy repo's .env", Tags: []string{"prod"}})
	c := created.Canary

	// Nothing in the token reveals a canary (ADR 0001): it parses as a plain
	// virtual key, with a key id that is 8 base62 characters like any other.
	tok, err := token.Parse(string(created.Secret))
	if err != nil || tok.Kind != token.VirtualKey || string(tok.ID) != c.ID || len(c.ID) != 8 {
		t.Fatalf("the secret parses as %+v (%v), for canary %s", tok, err, c.ID)
	}
	if c.Name != "decoy" || c.Planted != "the deploy repo's .env" || c.State != "armed" || c.Fires != 0 || c.FiredAt != nil || c.LastFiredAt != nil ||
		c.LastUse != nil || len(c.Tags) != 1 || c.Tags[0] != "prod" || c.Ignore == nil || len(c.Ignore) != 0 || !c.CreatedAt.Equal(t0) || !c.ArmedAt.Equal(t0) {
		t.Errorf("canary = %+v", c)
	}

	// The key is a canary on the server: no credential, never expiring (an
	// expired canary would answer key_expired and never raise its alert), and
	// the request path has it with the secret's hash.
	err = in.store.Do(t.Context(), func(tx *store.Tx) error {
		k, err := tx.Key(c.ID)
		if err != nil {
			return err
		}
		if k.Kind != store.KeyCanary || k.CredentialID != "" || !k.ExpiresAt.IsZero() || k.Status != store.KeyActive {
			t.Errorf("stored key = %+v, want an active canary with no credential and no expiry", k)
		}
		secrets, err := tx.KeySecrets(c.ID)
		if err != nil || len(secrets) != 1 || string(secrets[0].Hash) != string(tok.Hash[:]) {
			t.Errorf("stored secrets = %+v (%v), want just the hash of the one shown", secrets, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	route, ok := in.reg.Lookup(c.ID)
	if !ok || route.Key.CredentialID != "" || !route.Key.ExpiresAt.IsZero() || route.Key.Status != registry.KeyActive || len(route.Key.Secrets) != 1 {
		t.Errorf("registry route = %+v, %v; want a key with no credential and no expiry", route, ok)
	}
}

func TestACanaryNeedsWhereItWasPlanted(t *testing.T) {
	in := newInstall(t)
	for name, req := range map[string]admin.CreateCanaryRequest{
		"no place":      {Name: "decoy"},
		"blank place":   {Planted: "   "},
		"control char":  {Planted: "the .env\nsecond line"},
		"too long":      {Planted: strings.Repeat("x", 201)},
		"bad name":      {Name: "Not A Name", Planted: "x"},
		"bad tag":       {Planted: "x", Tags: []string{"Bad Tag"}},
		"bad format":    {Planted: "x", Format: "yaml"},
		"bad var":       {Planted: "x", Var: "open ai key"},
		"bad base url":  {Planted: "x", BaseURL: "ftp://kman.example"},
		"base url cred": {Planted: "x", BaseURL: "https://user:pw@kman.example/v1"},
		"bad ignore":    {Planted: "x", Ignore: []string{"not an address"}},
		"dup ignore":    {Planted: "x", Ignore: []string{"192.0.2.1", "192.0.2.1"}},
	} {
		if _, err := in.svc.CreateCanary(as("owner"), req); code(err) != admin.CodeInvalid {
			t.Errorf("%s: CreateCanary = %v, want an invalid error", name, err)
		}
	}
	in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"})
	if _, err := in.svc.CreateCanary(as("owner"), admin.CreateCanaryRequest{Name: "decoy", Planted: "y"}); code(err) != admin.CodeConflict {
		t.Errorf("a second canary with the name = %v, want a conflict", err)
	}
}

func TestTheSnippetIsInTheFormatAskedForAndRevealsNothingButTheKey(t *testing.T) {
	in := newInstall(t)
	for _, tc := range []struct {
		format, base string
		check        func(t *testing.T, snippet, secret string)
	}{
		{"env", "", func(t *testing.T, s, secret string) {
			if s != "OPENAI_API_KEY="+secret+"\n" {
				t.Errorf("env snippet = %q", s)
			}
		}},
		{"", "", func(t *testing.T, s, secret string) {
			if s != "OPENAI_API_KEY="+secret+"\n" {
				t.Errorf("the default format is env: %q", s)
			}
		}},
		{"env", "https://kman.example/v1", func(t *testing.T, s, secret string) {
			if s != "OPENAI_BASE_URL=https://kman.example/v1\nOPENAI_API_KEY="+secret+"\n" {
				t.Errorf("env snippet with a base URL = %q", s)
			}
		}},
		{"shell", "", func(t *testing.T, s, secret string) {
			if s != "export OPENAI_API_KEY="+secret+"\n" {
				t.Errorf("shell snippet = %q", s)
			}
		}},
		{"shell", "https://kman.example/v1", func(t *testing.T, s, secret string) {
			if s != "export OPENAI_BASE_URL='https://kman.example/v1'\nexport OPENAI_API_KEY="+secret+"\n" {
				t.Errorf("shell snippet with a base URL = %q", s)
			}
		}},
		{"ci", "", func(t *testing.T, s, secret string) {
			if s != "gh secret set OPENAI_API_KEY --body "+secret+"\n" {
				t.Errorf("ci snippet = %q", s)
			}
		}},
		{"mcp", "https://kman.example/v1", func(t *testing.T, s, secret string) {
			var got struct {
				MCPServers map[string]struct {
					Env map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(s), &got); err != nil || len(got.MCPServers) != 1 {
				t.Fatalf("mcp snippet %q is not an mcpServers block: %v", s, err)
			}
			for server, v := range got.MCPServers {
				if server != "openai" || v.Env["OPENAI_API_KEY"] != secret || v.Env["OPENAI_BASE_URL"] != "https://kman.example/v1" || len(v.Env) != 2 {
					t.Errorf("mcp server %q = %+v", server, v)
				}
			}
		}},
		{"mcp", "", func(t *testing.T, s, secret string) {
			if !json.Valid([]byte(s)) || !strings.Contains(s, `"OPENAI_API_KEY": "`+secret+`"`) || strings.Contains(s, "BASE_URL") {
				t.Errorf("mcp snippet = %q", s)
			}
		}},
	} {
		created := in.createCanary(t, admin.CreateCanaryRequest{Planted: "the deploy repo", Format: tc.format, BaseURL: tc.base, Tags: []string{"honey"}})
		tc.check(t, created.Snippet, string(created.Secret))
		if want := cmpOr(tc.format, "env"); created.Format != want {
			t.Errorf("format = %q, want %q", created.Format, want)
		}
		// What a thief reads must not say it is a canary: not the word, and not
		// anything the operator wrote down about it.
		for _, leak := range []string{"canary", "decoy", "honey", "deploy", "planted"} {
			if strings.Contains(strings.ToLower(created.Snippet), leak) {
				t.Errorf("the %s snippet says %q:\n%s", tc.format, leak, created.Snippet)
			}
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestTheSnippetNamesTheVariableAskedFor(t *testing.T) {
	in := newInstall(t)
	created := in.createCanary(t, admin.CreateCanaryRequest{Planted: "x", Var: "ANTHROPIC_API_KEY", Format: "shell", BaseURL: "https://kman.example"})
	want := "export ANTHROPIC_BASE_URL='https://kman.example'\nexport ANTHROPIC_API_KEY=" + string(created.Secret) + "\n"
	if created.Snippet != want {
		t.Errorf("snippet = %q, want %q", created.Snippet, want)
	}
	created = in.createCanary(t, admin.CreateCanaryRequest{Planted: "x", Var: "MY_TOKEN", BaseURL: "https://kman.example"})
	if !strings.HasPrefix(created.Snippet, "MY_TOKEN_BASE_URL=https://kman.example\n") {
		t.Errorf("a variable that does not end in _API_KEY gets _BASE_URL added: %q", created.Snippet)
	}
}

func TestTheCanarySecretIsNeverShownAgainOrWrittenToTheAuditLog(t *testing.T) {
	in := newInstall(t)
	created := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"})
	secret := string(created.Secret)
	shows := func(v any) {
		t.Helper()
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), secret) || strings.Contains(string(b), secret[len(secret)-12:]) {
			t.Errorf("a canary's secret shows again: %s", b)
		}
	}
	list, err := in.svc.ListCanaries(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	shows(list)
	got, err := in.svc.GetCanary(as("owner"), admin.CanaryRef{ID: created.Canary.ID})
	if err != nil {
		t.Fatal(err)
	}
	shows(got)
	for _, e := range in.latest(t, 10) {
		shows(e)
	}
	if fmtd := created.Secret.String(); strings.Contains(fmtd, secret) {
		t.Errorf("the secret prints as %q", fmtd)
	}
}

func TestCreatingAResettingAndUpdatingACanaryIsAudited(t *testing.T) {
	in := newInstall(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "the wiki"}).Canary
	in.fire(t, c.ID, t0.Add(time.Hour), store.CanaryUse{})
	in.now = t0.Add(2 * time.Hour)
	if _, err := in.svc.ResetCanary(as("owner"), admin.CanaryRef{ID: c.ID}); err != nil {
		t.Fatal(err)
	}
	planted := "the shared drive"
	if _, err := in.svc.UpdateCanary(as("owner"), admin.CanaryUpdate{ID: c.ID, Planted: &planted}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range in.latest(t, 3) {
		got = append(got, summary(e))
	}
	want := []string{"owner canary.create canary/" + c.ID, "owner canary.reset canary/" + c.ID, "owner canary.update canary/" + c.ID}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("audit = %v, want %v", got, want)
	}
}

func TestAResetRearmsAFiredCanaryAndKeepsItsHistory(t *testing.T) {
	in, _ := withCredential(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "the wiki"}).Canary
	use := store.CanaryUse{Address: netip.MustParseAddr("203.0.113.7"), Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom", Client: "curl/8"}
	fired := t0.Add(time.Hour)
	in.fire(t, c.ID, fired, use)

	got, err := in.svc.GetCanary(as("owner"), admin.CanaryRef{ID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "fired" || got.FiredAt == nil || !got.FiredAt.Equal(fired) || got.LastFiredAt == nil || !got.LastFiredAt.Equal(fired) || got.Fires != 1 {
		t.Fatalf("fired canary = %+v", got)
	}

	in.now = t0.Add(3 * time.Hour)
	reset, err := in.svc.ResetCanary(as("owner"), admin.CanaryRef{ID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if reset.State != "armed" || reset.FiredAt != nil || !reset.ArmedAt.Equal(in.now) || reset.LastFiredAt == nil || !reset.LastFiredAt.Equal(fired) || reset.Fires != 1 {
		t.Errorf("reset canary = %+v, want armed again with when it last fired kept", reset)
	}
	// Resetting an armed canary changes nothing, and records nothing.
	n := len(in.latest(t, 100))
	if _, err := in.svc.ResetCanary(as("owner"), admin.CanaryRef{ID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if after := len(in.latest(t, 100)); after != n {
		t.Errorf("resetting an armed canary added %d audit entries", after-n)
	}
	if _, err := in.svc.ResetCanary(as("owner"), admin.CanaryRef{ID: "nokey123"}); code(err) != admin.CodeNotFound {
		t.Errorf("reset of a canary that does not exist = %v, want not found", err)
	}
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	if _, err := in.svc.ResetCanary(as("owner"), admin.CanaryRef{ID: k.ID}); code(err) != admin.CodeNotFound {
		t.Errorf("reset of a standard key = %v, want not found: it is not a canary", err)
	}
}

func TestTheAPIWithholdsTheAddressOfTheLastUseUnlessClientAddressesAreOn(t *testing.T) {
	in := newInstall(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "the wiki"}).Canary
	in.fire(t, c.ID, t0.Add(time.Hour), store.CanaryUse{Address: netip.MustParseAddr("203.0.113.7"), Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom", Client: "curl/8"})

	got, err := in.svc.GetCanary(as("owner"), admin.CanaryRef{ID: c.ID})
	if err != nil || got.LastUse == nil {
		t.Fatalf("GetCanary = %+v, %v", got, err)
	}
	u := got.LastUse
	if u.Address != "" || !u.AddressWithheld || u.Country != "DE" || u.ASN != 3320 || u.ASOrg != "Deutsche Telekom" || u.Client != "curl/8" {
		t.Errorf("last use with the setting off = %+v, want the address withheld and the rest said", u)
	}
	if got.State != "fired" || got.FiredAt == nil || got.LastFiredAt == nil {
		t.Errorf("a withheld address must still say it fired and when: %+v", got)
	}
	list, err := in.svc.ListCanaries(as("owner"))
	if err != nil || len(list.Canaries) != 1 || list.Canaries[0].LastUse == nil || list.Canaries[0].LastUse.Address != "" {
		t.Errorf("ListCanaries = %+v, %v; want the address withheld there too", list, err)
	}

	in.exposeClients(t, true)
	got, err = in.svc.GetCanary(as("owner"), admin.CanaryRef{ID: c.ID})
	if err != nil || got.LastUse.Address != "203.0.113.7" || got.LastUse.AddressWithheld {
		t.Errorf("last use with the setting on = %+v, %v", got.LastUse, err)
	}
}

func TestTheIgnoreListIsNormalisedAndUpdatable(t *testing.T) {
	in := newInstall(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x", Ignore: []string{"192.0.2.0/24", " 198.51.100.7 ", "2001:DB8::/32"}}).Canary
	want := []string{"192.0.2.0/24", "198.51.100.7", "2001:db8::/32"}
	if strings.Join(c.Ignore, ",") != strings.Join(want, ",") {
		t.Errorf("ignore = %v, want %v", c.Ignore, want)
	}
	// An address that is not the network's own address is a mistake the operator
	// would like to hear about, but a prefix with host bits means the network.
	masked := in.createCanary(t, admin.CreateCanaryRequest{Planted: "x", Ignore: []string{"192.0.2.77/24"}}).Canary
	if len(masked.Ignore) != 1 || masked.Ignore[0] != "192.0.2.0/24" {
		t.Errorf("192.0.2.77/24 is kept as %v, want its network", masked.Ignore)
	}
	// A list this long is not an ignore list: it would silence the canary.
	tooMany := make([]string, 0, 101)
	for i := range 101 {
		tooMany = append(tooMany, netip.AddrFrom4([4]byte{10, 0, byte(i), 1}).String())
	}
	if _, err := in.svc.CreateCanary(as("owner"), admin.CreateCanaryRequest{Planted: "x", Ignore: tooMany}); code(err) != admin.CodeInvalid {
		t.Errorf("an ignore list of 101 = %v, want invalid", err)
	}
	// Everything is not an address to ignore: 0.0.0.0/0 would silence the canary.
	for _, wide := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/7", "2001::/15"} {
		if _, err := in.svc.CreateCanary(as("owner"), admin.CreateCanaryRequest{Planted: "x", Ignore: []string{wide}}); code(err) != admin.CodeInvalid {
			t.Errorf("ignore %s = %v, want invalid: it would silence the canary", wide, err)
		}
	}

	next := []string{"203.0.113.0/24"}
	updated, err := in.svc.UpdateCanary(as("owner"), admin.CanaryUpdate{ID: c.ID, Ignore: &next})
	if err != nil || len(updated.Ignore) != 1 || updated.Ignore[0] != "203.0.113.0/24" {
		t.Errorf("UpdateCanary = %+v, %v", updated, err)
	}
	none := []string{}
	updated, err = in.svc.UpdateCanary(as("owner"), admin.CanaryUpdate{ID: c.ID, Ignore: &none, Tags: &[]string{"a"}})
	if err != nil || len(updated.Ignore) != 0 || len(updated.Tags) != 1 {
		t.Errorf("UpdateCanary clearing the list = %+v, %v", updated, err)
	}
	if _, err := in.svc.UpdateCanary(as("owner"), admin.CanaryUpdate{ID: c.ID}); code(err) != admin.CodeInvalid {
		t.Errorf("an update that changes nothing = %v, want invalid", err)
	}
}

func TestACanaryIsNotAKeyAtRisk(t *testing.T) {
	in, _ := withCredential(t)
	app := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai", Policy: locked("203.0.113.0/24")}).Key
	in.used(t, app.ID, t0)
	before := in.exposure(t)

	// A canary has no address lock, never expires, and is never used by anyone
	// who should: each of the three things exposure looks for. None counts.
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"}).Canary
	in.now = t0.Add(60 * days)
	after := in.exposure(t)
	if r := rulesOf(after, c.ID); len(r) != 0 {
		t.Errorf("a canary has exposure findings %v", r)
	}
	if after.KeysChecked != before.KeysChecked {
		t.Errorf("exposure checked %d keys with a canary, %d without: a canary is not a key", after.KeysChecked, before.KeysChecked)
	}
}

func TestKeyListLeavesCanariesOutAndTellsThemApartWhenAsked(t *testing.T) {
	in, _ := withCredential(t)
	app := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"}).Canary

	list, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{})
	if err != nil || len(list.Keys) != 1 || list.Keys[0].ID != app.ID || list.Keys[0].Kind != "standard" {
		t.Fatalf("ListKeys = %+v, %v; want only the standard key", list, err)
	}
	// By name or id it is found, and says what it is.
	byName, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{Name: "decoy"})
	if err != nil || len(byName.Keys) != 1 || byName.Keys[0].ID != c.ID || byName.Keys[0].Kind != "canary" {
		t.Errorf("ListKeys(decoy) = %+v, %v", byName, err)
	}
	k, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: c.ID})
	if err != nil || k.Kind != "canary" || k.Expired {
		t.Errorf("GetKey(canary) = %+v, %v; want a canary that has not expired", k, err)
	}
}

func TestKeyCommandsThatWouldDisarmACanaryAreRefusedButRevokeRetiresIt(t *testing.T) {
	in := newInstall(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"}).Canary
	name := "other"
	for what, run := range map[string]func() error{
		"disable": func() error { _, err := in.svc.DisableKey(as("owner"), admin.KeyRef{ID: c.ID}); return err },
		"enable":  func() error { _, err := in.svc.EnableKey(as("owner"), admin.KeyRef{ID: c.ID}); return err },
		"rotate":  func() error { _, err := in.svc.RotateKey(as("owner"), admin.RotateKeyRequest{ID: c.ID}); return err },
		"update": func() error {
			_, err := in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: c.ID, Name: &name})
			return err
		},
	} {
		err := run()
		var ae *admin.Error
		if code(err) != admin.CodeConflict || !asAdminError(err, &ae) || !strings.Contains(ae.Message, "kman canary") {
			t.Errorf("key %s on a canary = %v, want a conflict that points at kman canary", what, err)
		}
	}
	// A disabled canary would raise a high alert for a disabled key instead of a
	// critical one, so it must still be armed.
	got, err := in.svc.GetCanary(as("owner"), admin.CanaryRef{ID: c.ID})
	if err != nil || got.State != "armed" {
		t.Errorf("canary after the refused commands = %+v, %v", got, err)
	}

	if _, err := in.svc.RevokeKey(as("owner"), admin.KeyRef{ID: c.ID}); err != nil {
		t.Fatal(err)
	}
	got, err = in.svc.GetCanary(as("owner"), admin.CanaryRef{ID: c.ID})
	if err != nil || got.State != "revoked" {
		t.Errorf("revoked canary = %+v, %v; want state revoked", got, err)
	}
	if _, err := in.svc.ResetCanary(as("owner"), admin.CanaryRef{ID: c.ID}); code(err) != admin.CodeConflict {
		t.Errorf("reset of a revoked canary = %v, want a conflict", err)
	}
	if _, err := in.svc.UpdateCanary(as("owner"), admin.CanaryUpdate{ID: c.ID, Ignore: &[]string{"192.0.2.1"}}); code(err) != admin.CodeConflict {
		t.Errorf("update of a revoked canary = %v, want a conflict", err)
	}
}

func asAdminError(err error, target **admin.Error) bool {
	ae, ok := err.(*admin.Error)
	*target = ae
	return ok
}

func TestAServiceReloadedFromTheStoreStillKnowsTheCanary(t *testing.T) {
	in := newInstall(t)
	c := in.createCanary(t, admin.CreateCanaryRequest{Name: "decoy", Planted: "x"}).Canary
	in.reg = registry.New()
	if err := in.service(nil).Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if route, ok := in.reg.Lookup(c.ID); !ok || route.Key.CredentialID != "" {
		t.Errorf("after Load the registry has %+v, %v for the canary", route, ok)
	}
}

func TestCanaryCommandsNeedACaller(t *testing.T) {
	in := newInstall(t)
	if _, err := in.svc.ListCanaries(t.Context()); code(err) != admin.CodeUnauthorized {
		t.Errorf("ListCanaries with no caller = %v", err)
	}
	if _, err := in.svc.CreateCanary(t.Context(), admin.CreateCanaryRequest{Planted: "x"}); code(err) != admin.CodeUnauthorized {
		t.Errorf("CreateCanary with no caller = %v", err)
	}
}
