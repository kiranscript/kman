package cli_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

// withOpenAI is a logged-in install with an openai upstream and a credential.
func withOpenAI(t *testing.T) *install {
	t.Helper()
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, secret+"\n", "upstream", "add", "openai", "--preset", "openai")
	return in
}

var printedKey = regexp.MustCompile(`kmn_[0-9A-Za-z]{8}_[0-9A-Za-z]{49}`)

func TestKeyCreatePrintsTheSecretOnceAndHowToUseIt(t *testing.T) {
	in := withOpenAI(t)
	out := in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	if !printedKey.MatchString(out) {
		t.Fatalf("no secret in:\n%s", out)
	}
	if !strings.Contains(out, "export OPENAI_API_KEY="+printedKey.FindString(out)) {
		t.Errorf("no export line in:\n%s", out)
	}
	if !strings.Contains(out, "only this once") || !strings.Contains(out, "2027-02-02") {
		t.Errorf("output lacks the warning or the expiry:\n%s", out)
	}
}

func TestKeyCreateTakesItsFlagsAndNoName(t *testing.T) {
	in := withOpenAI(t)
	var created admin.CreatedKey
	in.kmanJSON(t, &created, "key", "create", "--upstream", "openai", "--ttl", "30d", "--note", "billing", "--tag", "prod", "--tag", "team:ml", "--policy", "{}")
	k := created.Key
	if created.Secret == "" || k.Name != "" || k.Note != "billing" || strings.Join(k.Tags, ",") != "prod,team:ml" || k.ExpiresAt.Format("2006-01-02") != "2026-12-04" {
		t.Errorf("created = %+v", k)
	}
	in.kman(t, "key", "create", "named", "--upstream", "openai", "--credential", "openai")
}

func TestKeyCreateRefusesNeverAndAPastLifetime(t *testing.T) {
	in := withOpenAI(t)
	for _, ttl := range []string{"never", "366d", "0", "abc"} {
		r := in.run(t, nil, "", "key", "create", "--upstream", "openai", "--ttl", ttl)
		if r.code != 1 || printedKey.MatchString(r.stdout) {
			t.Errorf("--ttl %s: exit %d:\n%s%s", ttl, r.code, r.stdout, r.stderr)
		}
	}
	if r := in.run(t, nil, "", "key", "create", "--upstream", "openai", "--ttl", "never"); !strings.Contains(r.stderr, "never") {
		t.Errorf("the refusal of never says: %s", r.stderr)
	}
	if r := in.run(t, nil, "", "key", "create", "my-app"); r.code != 1 || !strings.Contains(r.stderr, "--upstream") {
		t.Errorf("no upstream: exit %d, %s", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "key", "create", "--upstream", "openai", "--policy", "{not json"); r.code != 1 || !strings.Contains(r.stderr, "--policy") {
		t.Errorf("bad JSON: exit %d, %s", r.code, r.stderr)
	}
}

func TestKeyShowAndUpdateTakeANameOrAKeyId(t *testing.T) {
	in := withOpenAI(t)
	var created admin.CreatedKey
	in.kmanJSON(t, &created, "key", "create", "my-app", "--upstream", "openai")
	id := created.Key.ID
	for _, ref := range []string{"my-app", id} {
		var k admin.Key
		in.kmanJSON(t, &k, "key", "show", ref)
		if k.ID != id {
			t.Errorf("key show %s = %+v", ref, k)
		}
		out := in.kman(t, "key", "show", ref)
		if !strings.Contains(out, id) || !strings.Contains(out, "my-app") || !strings.Contains(out, "openai") {
			t.Errorf("key show %s:\n%s", ref, out)
		}
		in.kman(t, "key", "update", ref, "--note", "by "+ref)
		in.kmanJSON(t, &k, "key", "show", id)
		if k.Note != "by "+ref {
			t.Errorf("after update %s, note = %q", ref, k.Note)
		}
	}
	if r := in.run(t, nil, "", "key", "show", "nothere"); r.code != 1 || !strings.Contains(r.stderr, "nothere") {
		t.Errorf("key show nothere: exit %d, %s", r.code, r.stderr)
	}
}

// A name that looks like another key's id still leaves that other key
// reachable: the id wins, so an operator's copy of a key id is never misdirected.
func TestAKeyIdWinsOverANameThatLooksLikeIt(t *testing.T) {
	in := withOpenAI(t)
	in.sqlExec(t, `INSERT INTO virtual_keys (key_id, credential_id, kind, status, policy_version, created_at, generation)
		SELECT 'abcd1234', id, 'standard', 'active', 1, 0, 1 FROM credentials`)
	var named admin.CreatedKey
	in.kmanJSON(t, &named, "key", "create", "abcd1234", "--upstream", "openai")
	var k admin.Key
	in.kmanJSON(t, &k, "key", "show", "abcd1234")
	if k.ID != "abcd1234" {
		t.Errorf("key show abcd1234 reached %s", k.ID)
	}
}

func TestKeyUpdateChangesEachField(t *testing.T) {
	in := withOpenAI(t)
	in.kman(t, "key", "create", "my-app", "--upstream", "openai", "--note", "old", "--tag", "a")
	var k admin.Key
	in.kmanJSON(t, &k, "key", "update", "my-app", "--name", "renamed", "--note", "new", "--tag", "b", "--tag", "c", "--ttl", "10d", "--policy", " { } ")
	if k.Name != "renamed" || k.Note != "new" || strings.Join(k.Tags, ",") != "b,c" || k.ExpiresAt.Format("2006-01-02") != "2026-11-14" || string(k.Policy) != "{}" {
		t.Errorf("key = %+v", k)
	}
	out := in.kman(t, "key", "update", "renamed", "--note", "")
	if !strings.Contains(out, "Updated") {
		t.Errorf("output: %s", out)
	}
	var cleared admin.Key
	in.kmanJSON(t, &cleared, "key", "update", "renamed", "--no-tags", "--name", "")
	if cleared.Name != "" || cleared.Note != "" || len(cleared.Tags) != 0 {
		t.Errorf("cleared key = %+v", cleared)
	}
	audit := in.kman(t, "audit", "export")
	if strings.Count(audit, `"key.update"`) != 3 {
		t.Errorf("audit log:\n%s", audit)
	}
}

func TestKeyUpdateRefusesWhatIsNotAnUpdate(t *testing.T) {
	in := withOpenAI(t)
	in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	for _, args := range [][]string{
		{"key", "update", "my-app"},
		{"key", "update", "my-app", "--ttl", "never"},
		{"key", "update", "my-app", "--ttl", "400d"},
		{"key", "update", "my-app", "--policy", `{"no_such_field":1}`},
		{"key", "update", "my-app", "--policy", `{not json`},
		{"key", "update", "my-app", "--tag", "a", "--no-tags"},
		{"key", "update"},
	} {
		if r := in.run(t, nil, "", args...); r.code != 1 {
			t.Errorf("kman %v: exit %d, %s%s", args, r.code, r.stdout, r.stderr)
		}
	}
}

func TestKeyListShowsEveryKeyAndItsState(t *testing.T) {
	in := withOpenAI(t)
	var a, b admin.CreatedKey
	in.kmanJSON(t, &a, "key", "create", "app-a", "--upstream", "openai", "--tag", "prod")
	in.kmanJSON(t, &b, "key", "create", "--upstream", "openai", "--ttl", "1h")
	in.kman(t, "key", "disable", a.Key.ID)
	out := in.kman(t, "key", "list")
	if !regexp.MustCompile(`ID\s+NAME\s+UPSTREAM\s+STATUS\s+EXPIRES\s+TAGS`).MatchString(out) ||
		!regexp.MustCompile(a.Key.ID+`\s+app-a\s+openai\s+disabled\s+2027-02-02T12:00Z\s+prod`).MatchString(out) ||
		!regexp.MustCompile(b.Key.ID+`\s+-\s+openai\s+active\s+2026-11-04T13:00Z`).MatchString(out) {
		t.Errorf("key list:\n%s", out)
	}
	var list admin.KeyList
	in.kmanJSON(t, &list, "key", "list")
	if len(list.Keys) != 2 {
		t.Errorf("key list --json = %+v", list)
	}
}

// The secret is printed by key create and by nothing after it.
func TestTheSecretAppearsOnlyInKeyCreate(t *testing.T) {
	in := withOpenAI(t)
	out := in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	secret := printedKey.FindString(out)
	if secret == "" {
		t.Fatalf("no secret in:\n%s", out)
	}
	var seen strings.Builder
	for _, args := range [][]string{
		{"key", "list"},
		{"key", "list", "--json"},
		{"key", "show", "my-app"},
		{"key", "show", "my-app", "--json"},
		{"key", "update", "my-app", "--note", "changed"},
		{"key", "update", "my-app", "--note", "again", "--json"},
		{"audit"},
		{"audit", "--json"},
		{"audit", "export"},
	} {
		r := in.run(t, nil, "", args...)
		if r.code != 0 {
			t.Errorf("kman %v: exit %d, %s", args, r.code, r.stderr)
		}
		seen.WriteString(r.stdout + r.stderr)
	}
	seen.WriteString(in.status(t, admin.PriceDataStatus{}))
	seen.WriteString(in.logs.String())
	if strings.Contains(seen.String(), secret) {
		t.Errorf("the secret was shown again:\n%s", seen.String())
	}
}

// How an operator watches a key's spend in v0.1.0, with events and metrics deferred.
func TestKeyShowPrintsTheSpendTodayAndThisMonthBesideTheCaps(t *testing.T) {
	in := withOpenAI(t)
	in.withSpend(t, func(keyID, policy string) admin.KeySpend {
		return admin.KeySpend{
			Today: admin.SpendPeriod{Period: "2026-11-04", CostMicro: 1_500_000, EstimatedMicro: 2_500, ReservedMicro: 40_000, Requests: 3, UnmeteredRequests: 2, CapMicro: 5_000_000},
			Month: admin.SpendPeriod{Period: "2026-11", CostMicro: 9_250_000, Requests: 30},
		}
	})
	in.kman(t, "key", "create", "my-app", "--upstream", "openai", "--policy", `{"daily_cap_micro":5000000}`)
	out := in.kman(t, "key", "show", "my-app")
	for _, want := range []string{
		`spend today:\s+\$1\.50 of \$5 cap \+ 2 unmetered requests \(2026-11-04: 3 requests, \$0\.0025 estimated, \$0\.04 in flight\)`,
		`spend month:\s+\$9\.25, no cap \(2026-11: 30 requests\)`,
	} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("key show lacks %q:\n%s", want, out)
		}
	}
	var k admin.Key
	in.kmanJSON(t, &k, "key", "show", "my-app")
	if k.Spend == nil || k.Spend.Today.CostMicro != 1_500_000 || k.Spend.Today.CapMicro != 5_000_000 || k.Spend.Month.Period != "2026-11" {
		t.Errorf("key show --json spend = %+v", k.Spend)
	}
	var list admin.KeyList
	in.kmanJSON(t, &list, "key", "list")
	if len(list.Keys) != 1 || list.Keys[0].Spend == nil || list.Keys[0].Spend.Month.Requests != 30 {
		t.Errorf("key list --json = %+v", list)
	}
}

func TestKeyShowSaysNothingOfSpendWhenThereIsNoMeter(t *testing.T) {
	in := withOpenAI(t)
	in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	if out := in.kman(t, "key", "show", "my-app"); strings.Contains(out, "spend") {
		t.Errorf("key show:\n%s", out)
	}
}

func TestKeyDisableEnableAndRevokeTakeANameOrAKeyId(t *testing.T) {
	in := withOpenAI(t)
	var k admin.CreatedKey
	in.kmanJSON(t, &k, "key", "create", "my-app", "--upstream", "openai")
	status := func() string {
		t.Helper()
		var got admin.Key
		in.kmanJSON(t, &got, "key", "show", k.Key.ID)
		return got.Status
	}

	out := in.kman(t, "key", "disable", "my-app")
	if !strings.Contains(out, "Disabled key my-app ("+k.Key.ID+")") || status() != "disabled" {
		t.Errorf("disable printed %q, status %s", out, status())
	}
	out = in.kman(t, "key", "enable", k.Key.ID)
	if !strings.Contains(out, "Enabled key my-app") || status() != "active" {
		t.Errorf("enable printed %q, status %s", out, status())
	}
	in.kman(t, "key", "disable", "my-app")
	out = in.kman(t, "key", "revoke", "my-app")
	if !strings.Contains(out, "Revoked key my-app") || status() != "revoked" {
		t.Errorf("revoke printed %q, status %s", out, status())
	}
	var shown admin.Key
	in.kmanJSON(t, &shown, "key", "revoke", k.Key.ID)
	if shown.Status != "revoked" || shown.ID != k.Key.ID {
		t.Errorf("revoke --json = %+v", shown)
	}
	if r := in.run(t, nil, "", "key", "list"); !regexp.MustCompile(k.Key.ID + `\s+my-app\s+openai\s+revoked`).MatchString(r.stdout) {
		t.Errorf("key list:\n%s", r.stdout)
	}
}

func TestARevokedKeyCannotBeEnabledFromTheCLI(t *testing.T) {
	in := withOpenAI(t)
	var k admin.CreatedKey
	in.kmanJSON(t, &k, "key", "create", "my-app", "--upstream", "openai")
	in.kman(t, "key", "revoke", k.Key.ID)
	r := in.run(t, nil, "", "key", "enable", k.Key.ID)
	if r.code != 1 || !strings.Contains(r.stderr, "revoked") {
		t.Errorf("enable of a revoked key: exit %d, %s%s", r.code, r.stdout, r.stderr)
	}
}

func TestKeyLifecycleCommandsNeedOneKeyAndKnowIt(t *testing.T) {
	in := withOpenAI(t)
	for _, action := range []string{"disable", "enable", "revoke"} {
		if r := in.run(t, nil, "", "key", action); r.code != 1 {
			t.Errorf("key %s with no key: exit %d, %s%s", action, r.code, r.stdout, r.stderr)
		}
		if r := in.run(t, nil, "", "key", action, "nobody"); r.code != 1 || !strings.Contains(r.stderr, "nobody") {
			t.Errorf("key %s nobody: exit %d, %s", action, r.code, r.stderr)
		}
	}
}
