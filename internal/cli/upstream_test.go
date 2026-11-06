package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
)

const secret = "sk-live-sentinel-0123456789abcdefWXYZ"

var ownerCtx = admin.WithCaller(context.Background(), admin.Caller{Actor: "owner"})

// kmanIn runs the CLI with stdin as its input and fails the test unless it
// exits 0.
func (in *install) kmanIn(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	r := in.run(t, nil, stdin, args...)
	if r.code != 0 {
		t.Fatalf("kman %s exited %d:\n%s%s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func TestUpstreamAddFromAPresetAsksForTheCredential(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	r := in.kmanIn(t, secret+"\n", "upstream", "add", "openai", "--preset", "openai")
	for _, want := range []string{"openai", "api.openai.com", "kman key create"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "openai")
	if u.Preset != "openai" || u.PresetVersion != 1 || u.Host != "api.openai.com" || !u.TLS || u.Auth.Header != "Authorization: Bearer {key}" || u.SDKEnv != "OPENAI_API_KEY" {
		t.Errorf("show --json = %+v", u)
	}
	if len(u.Credentials) != 1 || u.Credentials[0].Name != "openai" {
		t.Errorf("credentials = %+v", u.Credentials)
	}
}

func TestUpstreamAddNamesItAfterThePresetByDefault(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, secret+"\n", "upstream", "add", "--preset", "groq")
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "groq")
	if u.Host != "api.groq.com" {
		t.Errorf("show = %+v", u)
	}
}

func TestUpstreamAddReadsTheCredentialFromTheNamedVariable(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	env := map[string]string{"OPENAI_API_KEY": secret + "\n"}
	r := in.run(t, env, "", "upstream", "add", "openai", "--preset", "openai", "--credential-env", "OPENAI_API_KEY")
	if r.code != 0 {
		t.Fatalf("exited %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "openai")
	if len(u.Credentials) != 1 || u.Credentials[0].Last4 != "WXYZ" {
		t.Errorf("credentials = %+v", u.Credentials)
	}
	r = in.run(t, nil, "", "upstream", "add", "groq", "--preset", "groq", "--credential-env", "MISSING_VAR")
	if r.code != 1 || !strings.Contains(r.stderr, "MISSING_VAR") {
		t.Errorf("an unset variable: exit %d, %s", r.code, r.stderr)
	}
}

func TestUpstreamAddWithoutACredentialIsRefusedUnlessAskedFor(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	r := in.run(t, nil, "", "upstream", "add", "openai", "--preset", "openai")
	if r.code != 1 || !strings.Contains(r.stderr, "--no-credential") {
		t.Errorf("no input: exit %d, %s", r.code, r.stderr)
	}
	r = in.kmanIn(t, "", "upstream", "add", "openai", "--preset", "openai", "--no-credential")
	if !strings.Contains(r.stdout, "kman credential add") {
		t.Errorf("output does not name the next command:\n%s", r.stdout)
	}
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "openai")
	if len(u.Credentials) != 0 {
		t.Errorf("credentials = %+v", u.Credentials)
	}
}

func TestACustomUpstreamIsBuiltWithFlags(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, "", "upstream", "add", "acme", "--no-credential", "--host", "api.acme.test", "--port", "8443",
		"--path", "POST /v1/things", "--path", "GET /v1/things/*", "--header", "X-Acme-Key: {key}",
		"--sdk-env", "ACME_KEY", "--sdk-base-path", "/v1", "--profile", "generic")
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "acme")
	if u.Preset != "" || u.Host != "api.acme.test" || u.Port != 8443 || !u.TLS || u.Auth != (admin.Auth{Style: "header", Header: "X-Acme-Key: {key}"}) ||
		strings.Join(u.Paths, ",") != "POST /v1/things,GET /v1/things/*" || u.SDKEnv != "ACME_KEY" || u.SDKBasePath != "/v1" {
		t.Errorf("show = %+v", u)
	}
}

func TestACustomUpstreamCanUseBasicAuth(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, "", "upstream", "add", "userpass", "--no-credential", "--host", "a.test", "--path", "GET /", "--basic")
	in.kmanIn(t, "", "upstream", "add", "withuser", "--no-credential", "--host", "b.test", "--path", "GET /", "--no-tls", "--basic-username", "api")
	var a, b admin.Upstream
	in.kmanJSON(t, &a, "upstream", "show", "userpass")
	in.kmanJSON(t, &b, "upstream", "show", "withuser")
	if a.Auth != (admin.Auth{Style: "basic"}) || b.Auth != (admin.Auth{Style: "basic", BasicUsername: "api"}) || b.TLS || b.Port != 80 {
		t.Errorf("auth = %+v, %+v; %v %d", a.Auth, b.Auth, b.TLS, b.Port)
	}
	r := in.run(t, nil, "", "upstream", "add", "both", "--no-credential", "--host", "c.test", "--path", "GET /", "--basic", "--header", "X: {key}")
	if r.code != 1 {
		t.Errorf("--basic and --header: exit %d, %s", r.code, r.stderr)
	}
}

func TestUpstreamListAndShowPrintTheUpstream(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, secret+"\n", "upstream", "add", "openai", "--preset", "openai")
	in.kmanIn(t, "", "upstream", "add", "acme", "--no-credential", "--host", "api.acme.test", "--path", "POST /v1/things")

	out := in.kman(t, "upstream", "list")
	for _, want := range []string{"NAME", "acme", "custom", "api.acme.test:443", "openai v1", "api.openai.com:443"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	var list admin.UpstreamList
	in.kmanJSON(t, &list, "upstream", "list")
	if len(list.Upstreams) != 2 || list.Upstreams[0].Name != "acme" {
		t.Errorf("list --json = %+v", list)
	}
	out = in.kman(t, "upstream", "show", "openai")
	for _, want := range []string{"api.openai.com", "POST /v1/chat/completions", "Authorization: Bearer {key}", "OPENAI_API_KEY", "openai", "WXYZ"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
}

func TestUpstreamUpdateChangesOnlyTheFlagsGiven(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, "", "upstream", "add", "openai", "--preset", "openai", "--no-credential")
	in.kman(t, "upstream", "update", "openai", "--host", "gw.example.com", "--name", "primary", "--no-inject-usage")
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "primary")
	if u.Host != "gw.example.com" || u.InjectUsage || u.Port != 443 || u.SDKEnv != "OPENAI_API_KEY" {
		t.Errorf("show = %+v", u)
	}
	r := in.run(t, nil, "", "upstream", "update", "nope", "--host", "a.test")
	if r.code != 1 || !strings.Contains(r.stderr, "nope") {
		t.Errorf("a missing upstream: exit %d, %s", r.code, r.stderr)
	}
	r = in.run(t, nil, "", "upstream", "update", "primary")
	if r.code != 1 {
		t.Errorf("no flags: exit %d, %s", r.code, r.stderr)
	}
}

func TestRmRefusesWhileAKeyDependsAndWorksOtherwise(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, secret+"\n", "upstream", "add", "openai", "--preset", "openai")
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "openai")
	in.seedKey(t, "abcd1234", u.Credentials[0].ID, "active")
	for _, args := range [][]string{{"upstream", "rm", "openai"}, {"credential", "rm", "openai"}} {
		r := in.run(t, nil, "", args...)
		if r.code != 1 || !strings.Contains(r.stderr, "abcd1234") {
			t.Errorf("kman %v: exit %d, %s", args, r.code, r.stderr)
		}
	}
	in.seedKeyStatus(t, "abcd1234", "revoked")
	out := in.kman(t, "upstream", "rm", "openai")
	if !strings.Contains(out, "openai") {
		t.Errorf("output: %s", out)
	}
	if r := in.run(t, nil, "", "upstream", "show", "openai"); r.code != 1 {
		t.Errorf("show after rm: exit %d", r.code)
	}
}

func TestCredentialAddUpdateAndRm(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, "", "upstream", "add", "openai", "--preset", "openai", "--no-credential")

	r := in.kmanIn(t, secret+"\n", "credential", "add", "main", "--upstream", "openai")
	if strings.Contains(r.stdout+r.stderr, secret) {
		t.Errorf("add printed the secret:\n%s%s", r.stdout, r.stderr)
	}
	show := func() admin.Credential {
		var u admin.Upstream
		in.kmanJSON(t, &u, "upstream", "show", "openai")
		if len(u.Credentials) != 1 {
			t.Fatalf("credentials = %+v", u.Credentials)
		}
		return u.Credentials[0]
	}
	if c := show(); c.Name != "main" || c.Last4 != "WXYZ" {
		t.Errorf("after add: %+v", c)
	}

	in.kmanIn(t, "sk-live-replacement-0123456789abcdefQRST\n", "credential", "update", "main")
	if c := show(); c.Last4 != "QRST" || c.RotatedAt == nil {
		t.Errorf("after replacing the secret: %+v", c)
	}
	in.kmanIn(t, "", "credential", "update", "main", "--name", "primary")
	if c := show(); c.Name != "primary" || c.Last4 != "QRST" {
		t.Errorf("after renaming: %+v", c)
	}
	env := map[string]string{"NEW_KEY": "sk-live-another-0123456789abcdefMNOP"}
	if r := in.run(t, env, "", "credential", "update", "primary", "--credential-env", "NEW_KEY"); r.code != 0 {
		t.Fatalf("update --credential-env: %s", r.stderr)
	}
	if c := show(); c.Last4 != "MNOP" {
		t.Errorf("after --credential-env: %+v", c)
	}
	in.kmanIn(t, "sk-live-prompted-0123456789abcdefIJKL\n", "credential", "update", "primary", "--name", "both", "--secret")
	if c := show(); c.Name != "both" || c.Last4 != "IJKL" {
		t.Errorf("after --name --secret: %+v", c)
	}

	in.kman(t, "credential", "rm", "both")
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "openai")
	if len(u.Credentials) != 0 {
		t.Errorf("after rm: %+v", u.Credentials)
	}
}

func TestCredentialAddNeedsItsUpstreamAndASecret(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, "", "upstream", "add", "openai", "--preset", "openai", "--no-credential")
	if r := in.run(t, nil, secret+"\n", "credential", "add", "main"); r.code != 1 || !strings.Contains(r.stderr, "--upstream") {
		t.Errorf("no upstream: exit %d, %s", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "credential", "add", "main", "--upstream", "openai"); r.code != 1 {
		t.Errorf("no secret: exit %d, %s", r.code, r.stderr)
	}
}

// catalog is one preset, "acme", at version 1, and its next version.
func catalog() (v1, v2 []registry.Preset) {
	spec := registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.acme.test", Port: 443, TLS: true,
		Paths: []string{"GET /v1/a", "GET /v1/b"},
		Auth:  registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"},
	}
	next := spec
	next.Host = "api2.acme.test"
	next.Paths = []string{"GET /v1/a", "POST /v1/c"}
	return []registry.Preset{{Name: "acme", Title: "Acme", Version: 1, Spec: spec}},
		[]registry.Preset{{Name: "acme", Title: "Acme", Version: 2, Spec: next}}
}

// upgradedInstall is an install whose server runs acme v2, with an upstream
// made from acme v1, as after an upgrade.
func upgradedInstall(t *testing.T) *install {
	t.Helper()
	v1, v2 := catalog()
	in := newInstallWith(t, v1)
	if _, err := in.svc.AddUpstream(ownerCtx, admin.AddUpstreamRequest{Name: "acme", Preset: "acme"}); err != nil {
		t.Fatal(err)
	}
	in.loggedIn(t)
	in.upgrade(t, v2)
	return in
}

func TestUpstreamSyncShowsTheDifferenceAndAppliesItOnConfirmation(t *testing.T) {
	in := upgradedInstall(t)
	if out := in.kman(t, "upstream", "list"); !strings.Contains(out, "update available") {
		t.Errorf("list does not offer the update:\n%s", out)
	}
	r := in.kmanIn(t, "y\n", "upstream", "sync", "acme")
	for _, want := range []string{"version 1", "version 2", "api.acme.test", "api2.acme.test", "GET /v1/b", "POST /v1/c"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("sync output lacks %q:\n%s", want, r.stdout)
		}
	}
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "acme")
	if u.Host != "api2.acme.test" || u.PresetVersion != 2 {
		t.Errorf("after sync: %+v", u)
	}
	r = in.kmanIn(t, "", "upstream", "sync", "acme")
	if !strings.Contains(r.stdout, "up to date") {
		t.Errorf("a second sync says:\n%s", r.stdout)
	}
}

func TestUpstreamSyncChangesNothingUnlessConfirmed(t *testing.T) {
	in := upgradedInstall(t)
	if r := in.kmanIn(t, "n\n", "upstream", "sync", "acme"); !strings.Contains(r.stdout, "Nothing changed") {
		t.Errorf("declining says:\n%s", r.stdout)
	}
	if r := in.run(t, nil, "", "upstream", "sync", "acme"); r.code != 1 || !strings.Contains(r.stderr, "--yes") {
		t.Errorf("no answer: exit %d, %s", r.code, r.stderr)
	}
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "acme")
	if u.Host != "api.acme.test" || u.PresetVersion != 1 {
		t.Errorf("after declining: %+v", u)
	}
}

func TestUpstreamSyncYesAppliesWithoutAsking(t *testing.T) {
	in := upgradedInstall(t)
	in.kman(t, "upstream", "sync", "acme", "--yes")
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "acme")
	if u.PresetVersion != 2 {
		t.Errorf("after --yes: %+v", u)
	}
}

func TestUpstreamSyncJSONPrintsThePlanUnlessToldToApply(t *testing.T) {
	in := upgradedInstall(t)
	var plan admin.UpstreamSyncPlan
	in.kmanJSON(t, &plan, "upstream", "sync", "acme")
	if plan.FromVersion != 1 || plan.ToVersion != 2 || len(plan.Changes) != 3 {
		t.Errorf("plan = %+v", plan)
	}
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "acme")
	if u.PresetVersion != 1 {
		t.Errorf("--json applied the plan: %+v", u)
	}
	var synced admin.Upstream
	in.kmanJSON(t, &synced, "upstream", "sync", "acme", "--yes")
	if synced.PresetVersion != 2 {
		t.Errorf("--json --yes = %+v", synced)
	}
}

// TestACredentialNeverAppearsInAnyOutput is the ticket's sweep: it drives
// every command that touches a credential through the CLI and the real API,
// including the ones that fail, and looks for the secret in everything kman
// says: CLI output, API responses (--json), log lines and audit entries.
func TestACredentialNeverAppearsInAnyOutput(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	var said strings.Builder
	run := func(stdin string, env map[string]string, args ...string) {
		t.Helper()
		r := in.run(t, env, stdin, args...)
		said.WriteString(r.stdout + r.stderr)
	}
	replacement := "sk-live-replacement-0123456789abcdefQRST"
	env := map[string]string{"THE_KEY": secret, "THE_OTHER_KEY": replacement}

	run(secret+"\n", nil, "upstream", "add", "openai", "--preset", "openai")
	run(secret+"\n", nil, "upstream", "add", "groq", "--preset", "groq", "--json")
	run("", env, "upstream", "add", "mistral", "--preset", "mistral", "--credential-env", "THE_KEY", "--json")
	run(secret+"\n", nil, "credential", "add", "second", "--upstream", "openai")
	run(secret+"\n", nil, "credential", "add", "third", "--upstream", "openai", "--json")
	run("", env, "credential", "add", "fourth", "--upstream", "openai", "--credential-env", "THE_KEY")
	run(replacement+"\n", nil, "credential", "update", "second")
	run("", env, "credential", "update", "third", "--credential-env", "THE_OTHER_KEY", "--json")
	run("", nil, "credential", "update", "fourth", "--name", "renamed", "--json")
	// The failures: a taken name, a bad name, a missing upstream, and a secret
	// the upstream's auth refuses.
	run(secret+"\n", nil, "upstream", "add", "openai", "--preset", "openai")
	run(secret+"\n", nil, "credential", "add", "second", "--upstream", "openai")
	run(secret+"\n", nil, "credential", "add", "Bad Name", "--upstream", "openai")
	run(secret+"\n", nil, "credential", "add", "fifth", "--upstream", "nope")
	run(secret+"\n", nil, "upstream", "add", "twilio", "--preset", "twilio")
	run(secret+"\n", nil, "upstream", "add", "--preset", "nope")
	// Every read.
	run("", nil, "upstream", "list")
	run("", nil, "upstream", "list", "--json")
	run("", nil, "upstream", "show", "openai")
	run("", nil, "upstream", "show", "openai", "--json")
	run("", nil, "audit")
	run("", nil, "audit", "--json")
	run("", nil, "audit", "export")
	// And the deletes.
	run("", nil, "credential", "rm", "second", "--json")
	run("", nil, "upstream", "rm", "openai", "--json")
	run("", nil, "upstream", "rm", "groq")
	run("", nil, "audit", "export")

	if !strings.Contains(said.String(), "credential.create") || !strings.Contains(said.String(), "credential.delete") {
		t.Fatalf("the sweep missed the audit log:\n%s", said.String())
	}
	var audit strings.Builder
	var after int64
	for {
		page, err := in.svc.Audit(ownerCtx, admin.AuditQuery{After: after, Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entries {
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			audit.Write(b)
			after = e.ID
		}
		if !page.More {
			break
		}
	}
	for where, text := range map[string]string{"the CLI's output": said.String(), "a log line": in.logs.String(), "an audit entry": audit.String()} {
		for _, s := range []string{secret, replacement} {
			if strings.Contains(text, s) {
				t.Errorf("%s holds a secret:\n%s", where, text)
			}
		}
	}
	// Nor is it at rest in the clear, in the database or its WAL.
	files, err := filepath.Glob(filepath.Join(in.dataDir, store.FileName+"*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no database files: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{secret, replacement} {
			if strings.Contains(string(b), s) {
				t.Errorf("%s holds a secret in the clear", filepath.Base(f))
			}
		}
	}
}
