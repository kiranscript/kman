package cli_test

import (
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

// fireCanary records a use of the canary, as the priority lane does.
func (in *install) fireCanary(t *testing.T, id string, at time.Time, use store.CanaryUse) {
	t.Helper()
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		_, _, err := tx.RecordCanaryUse(id, at, use)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCanaryCreatePrintsTheSnippetToPlantOnce(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "canary", "create", "decoy", "--planted", "the deploy repo's .env")
	secret := printedKey.FindString(out)
	if secret == "" {
		t.Fatalf("no secret in:\n%s", out)
	}
	for _, want := range []string{"decoy", "the deploy repo's .env", "only this once", "\nOPENAI_API_KEY=" + secret + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, secret) != 1 {
		t.Errorf("the secret is printed %d times, want once:\n%s", strings.Count(out, secret), out)
	}
	// It is never shown again.
	for _, args := range [][]string{{"canary", "list"}, {"canary", "show", "decoy"}, {"key", "show", "decoy"}, {"audit"}} {
		if got := in.kman(t, args...); strings.Contains(got, secret) || strings.Contains(got, secret[len(secret)-12:]) {
			t.Errorf("kman %s shows the secret again:\n%s", strings.Join(args, " "), got)
		}
	}
}

func TestCanaryCreateTakesEachFormat(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for format, want := range map[string]*regexp.Regexp{
		"env":   regexp.MustCompile(`(?m)^OPENAI_API_KEY=kmn_\w{8}_\w{49}$`),
		"shell": regexp.MustCompile(`(?m)^export OPENAI_API_KEY=kmn_\w{8}_\w{49}$`),
		"ci":    regexp.MustCompile(`(?m)^gh secret set OPENAI_API_KEY --body kmn_\w{8}_\w{49}$`),
		"mcp":   regexp.MustCompile(`(?s)"mcpServers": \{.*"env": \{\s*"OPENAI_API_KEY": "kmn_\w{8}_\w{49}"`),
	} {
		var created admin.CreatedCanary
		in.kmanJSON(t, &created, "canary", "create", "--planted", "x", "--format", format)
		if created.Format != format || !want.MatchString(created.Snippet) {
			t.Errorf("--format %s: snippet %q", format, created.Snippet)
		}
		if out := in.kman(t, "canary", "create", "--planted", "x", "--format", format); !want.MatchString(out) {
			t.Errorf("--format %s printed:\n%s", format, out)
		}
	}
}

func TestCanaryCreateTakesItsFlags(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	var created admin.CreatedCanary
	in.kmanJSON(t, &created, "canary", "create", "decoy", "--planted", "the wiki", "--tag", "prod", "--tag", "team:ml",
		"--ignore", "192.0.2.0/24", "--ignore", "198.51.100.7", "--var", "ANTHROPIC_API_KEY", "--base-url", "https://kman.example", "--format", "shell")
	c := created.Canary
	if c.Name != "decoy" || c.Planted != "the wiki" || strings.Join(c.Tags, ",") != "prod,team:ml" || strings.Join(c.Ignore, ",") != "192.0.2.0/24,198.51.100.7" {
		t.Errorf("canary = %+v", c)
	}
	if want := "export ANTHROPIC_BASE_URL='https://kman.example'\nexport ANTHROPIC_API_KEY=" + string(created.Secret) + "\n"; created.Snippet != want {
		t.Errorf("snippet = %q, want %q", created.Snippet, want)
	}
}

func TestCanaryCreateNeedsWhereItIsPlantedAndAKnownFormat(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	if r := in.run(t, nil, "", "canary", "create", "decoy"); r.code != 1 || !strings.Contains(r.stderr, "--planted") || printedKey.MatchString(r.stdout) {
		t.Errorf("no --planted: exit %d, %s%s", r.code, r.stdout, r.stderr)
	}
	if r := in.run(t, nil, "", "canary", "create", "--planted", "x", "--format", "yaml"); r.code != 1 || !strings.Contains(r.stderr, "format") {
		t.Errorf("--format yaml: exit %d, %s", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "canary", "create", "--planted", "x", "--ignore", "0.0.0.0/0"); r.code != 1 || !strings.Contains(r.stderr, "silence") {
		t.Errorf("--ignore 0.0.0.0/0: exit %d, %s", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "canary"); r.code != 1 || !strings.Contains(r.stderr, "create|list") {
		t.Errorf("kman canary: exit %d, %s", r.code, r.stderr)
	}
}

func TestCanaryListSaysWhereEachWasPlantedWhetherItFiredAndWhen(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	var quiet, loud admin.CreatedCanary
	in.kmanJSON(t, &quiet, "canary", "create", "quiet", "--planted", "the wiki")
	in.kmanJSON(t, &loud, "canary", "create", "loud", "--planted", "the deploy repo's .env")
	fired := time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC)
	in.fireCanary(t, loud.Canary.ID, fired, store.CanaryUse{Address: netip.MustParseAddr("203.0.113.7"), Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom", Client: "curl/8"})

	out := in.kman(t, "canary", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		t.Fatalf("canary list:\n%s", out)
	}
	// Canaries made in the same instant (the test's clock does not move) have
	// no order but their ids', so each is found by name.
	row := func(name string) string {
		for _, l := range lines[1:] {
			if f := strings.Fields(l); len(f) > 1 && f[1] == name {
				return l
			}
		}
		t.Fatalf("no row for %s in:\n%s", name, out)
		return ""
	}
	lines = []string{lines[0], row("quiet"), row("loud")}
	for _, head := range []string{"ID", "NAME", "STATE", "PLANTED", "LAST FIRED"} {
		if !strings.Contains(lines[0], head) {
			t.Errorf("the header lacks %s: %s", head, lines[0])
		}
	}
	if f := strings.Fields(lines[1]); f[1] != "quiet" || f[2] != "armed" || !strings.Contains(lines[1], "the wiki") || !strings.Contains(lines[1], "never") {
		t.Errorf("the canary that has not fired: %s", lines[1])
	}
	// With client addresses off, the address is withheld but the canary still
	// says that it fired and when, and what it knows of where.
	for _, want := range []string{"loud", "fired", "the deploy repo's .env", "2026-11-05T08:30Z", "DE", "AS3320", "curl/8"} {
		if !strings.Contains(lines[2], want) {
			t.Errorf("the canary that fired lacks %q: %s", want, lines[2])
		}
	}
	if strings.Contains(out, "203.0.113.7") {
		t.Errorf("the address is shown with expose_client_addresses off:\n%s", out)
	}
	in.kman(t, "settings", "set", "expose_client_addresses", "on")
	if out := in.kman(t, "canary", "list"); !strings.Contains(out, "203.0.113.7") {
		t.Errorf("the address is withheld with expose_client_addresses on:\n%s", out)
	}
	var list admin.CanaryList
	in.kmanJSON(t, &list, "canary", "list")
	if len(list.Canaries) != 2 {
		t.Errorf("canary list --json = %+v", list)
	}
	for _, k := range list.Canaries {
		if want := map[string]string{"quiet": "armed", "loud": "fired"}[k.Name]; k.State != want {
			t.Errorf("canary %s is %s, want %s", k.Name, k.State, want)
		}
	}
	if empty := newInstall(t).loggedIn(t).kman(t, "canary", "list"); !strings.Contains(empty, "No canaries") {
		t.Errorf("with none: %q", empty)
	}
}

func TestCanaryResetArmsAFiredCanaryByNameOrId(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	var created admin.CreatedCanary
	in.kmanJSON(t, &created, "canary", "create", "decoy", "--planted", "the wiki")
	id := created.Canary.ID
	for _, ref := range []string{"decoy", id} {
		in.fireCanary(t, id, time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC), store.CanaryUse{})
		var c admin.Canary
		in.kmanJSON(t, &c, "canary", "show", ref)
		if c.State != "fired" {
			t.Fatalf("canary show %s after a use = %+v", ref, c)
		}
		if out := in.kman(t, "canary", "reset", ref); !strings.Contains(out, "decoy") || !strings.Contains(out, "armed") {
			t.Errorf("canary reset %s:\n%s", ref, out)
		}
		in.kmanJSON(t, &c, "canary", "show", ref)
		if c.State != "armed" || c.LastFiredAt == nil {
			t.Errorf("canary show %s after a reset = %+v, want armed with its last use kept", ref, c)
		}
	}
	if out := in.kman(t, "canary", "reset", "decoy"); !strings.Contains(out, "already armed") {
		t.Errorf("reset of an armed canary: %q", out)
	}
	if r := in.run(t, nil, "", "canary", "reset", "nothere"); r.code != 1 || !strings.Contains(r.stderr, "nothere") {
		t.Errorf("reset of nothere: exit %d, %s", r.code, r.stderr)
	}
}

func TestCanaryUpdateChangesWhereItIsPlantedAndItsIgnoreList(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "canary", "create", "decoy", "--planted", "the wiki", "--ignore", "192.0.2.1", "--tag", "a")
	var c admin.Canary
	in.kmanJSON(t, &c, "canary", "update", "decoy", "--planted", "the drive", "--ignore", "198.51.100.0/24", "--ignore", "198.51.101.1")
	if c.Planted != "the drive" || strings.Join(c.Ignore, ",") != "198.51.100.0/24,198.51.101.1" || strings.Join(c.Tags, ",") != "a" {
		t.Errorf("canary = %+v", c)
	}
	in.kmanJSON(t, &c, "canary", "update", "decoy", "--no-ignore", "--no-tags")
	if len(c.Ignore) != 0 || len(c.Tags) != 0 || c.Planted != "the drive" {
		t.Errorf("canary after clearing = %+v", c)
	}
	if r := in.run(t, nil, "", "canary", "update", "decoy"); r.code != 1 || !strings.Contains(r.stderr, "usage") {
		t.Errorf("update with nothing: exit %d, %s", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "canary", "update", "decoy", "--ignore", "192.0.2.1", "--no-ignore"); r.code != 1 {
		t.Errorf("--ignore with --no-ignore: exit %d", r.code)
	}
}

func TestKeyListAndStatusLeaveCanariesOutAndKeyShowNamesOneAsACanary(t *testing.T) {
	in := withOpenAI(t)
	in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	in.kman(t, "canary", "create", "decoy", "--planted", "the wiki")
	list := in.kman(t, "key", "list")
	if strings.Contains(list, "decoy") || !strings.Contains(list, "my-app") {
		t.Errorf("key list:\n%s", list)
	}
	show := in.kman(t, "key", "show", "decoy")
	if !strings.Contains(show, "canary") || strings.Contains(show, "0001-01-01") || !strings.Contains(show, "never") {
		t.Errorf("key show of a canary:\n%s", show)
	}
	r := in.run(t, nil, "", "key", "disable", "decoy")
	if r.code != 1 || !strings.Contains(r.stderr, "kman canary") {
		t.Errorf("key disable of a canary: exit %d, %s", r.code, r.stderr)
	}
	// It is retired with key revoke.
	in.kman(t, "key", "revoke", "decoy")
	if out := in.kman(t, "canary", "list"); !strings.Contains(out, "revoked") {
		t.Errorf("a revoked canary:\n%s", out)
	}
	// And it is not a key at risk.
	if out := in.kman(t, "exposure"); strings.Contains(out, "decoy") {
		t.Errorf("exposure names a canary:\n%s", out)
	}
}
