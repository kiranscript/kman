package cli_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/admin"
	"github.com/kiranscript/kman/internal/cli"
	"github.com/kiranscript/kman/internal/detect"
	"github.com/kiranscript/kman/internal/store"
)

// withIncident is an install with a key named my-app and incident 1 for it: a
// high new-country detection from Germany, at t0.
func withIncident(t *testing.T) (*install, admin.Key) {
	t.Helper()
	in := withOpenAI(t)
	in.now = func() time.Time { return t0 }
	var created admin.CreatedKey
	in.kmanJSON(t, &created, "key", "create", "my-app", "--upstream", "openai")
	in.detect(t, created.Key.ID, "new_country", store.SeverityHigh, store.Evidence{
		Address: "203.0.113.7", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Client: "curl/8", Observed: "DE", Baseline: "US",
	})
	return in, created.Key
}

func (in *install) detect(t *testing.T, keyID, rule string, sev store.Severity, ev store.Evidence) {
	t.Helper()
	in.detectAt(t, t0, keyID, rule, sev, ev)
}

// detectAt raises a detection at a time of the test's choosing.
func (in *install) detectAt(t *testing.T, at time.Time, keyID, rule string, sev store.Severity, ev store.Evidence) detect.Outcome {
	t.Helper()
	e := detect.New(detect.Options{Store: in.store, Now: func() time.Time { return at }})
	out, err := e.RaiseNow(t.Context(), detect.Detection{KeyID: keyID, Rule: rule, Severity: sev, At: at, Evidence: ev})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIncidentsListsThemNewestFirstAndOpenOnesOnRequest(t *testing.T) {
	in, k := withIncident(t)
	if out := in.kman(t, "incident", "resolve", "1"); !strings.Contains(out, "Resolved incident 1") {
		t.Fatalf("resolve:\n%s", out)
	}
	in.detect(t, k.ID, "new_asn", store.SeverityLow, store.Evidence{ASN: 1})

	out := in.kman(t, "incidents")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") {
		t.Fatalf("kman incidents:\n%s", out)
	}
	for _, col := range []string{"KEY", "STATE", "SEVERITY", "RULES", "DETECTIONS", "OPENED", "LAST FIRED"} {
		if !strings.Contains(lines[0], col) {
			t.Errorf("the header lacks %s: %s", col, lines[0])
		}
	}
	if !strings.HasPrefix(lines[1], "2") || !strings.Contains(lines[1], "my-app") || !strings.Contains(lines[1], "open") || !strings.Contains(lines[1], "low") ||
		!strings.Contains(lines[2], "resolved (confirmed)") || !strings.Contains(lines[2], "new_country") {
		t.Errorf("rows:\n%s", out)
	}
	open := in.kman(t, "incidents", "--open")
	if strings.Contains(open, "new_country") || !strings.Contains(open, "new_asn") {
		t.Errorf("--open:\n%s", open)
	}
	var list admin.IncidentList
	in.kmanJSON(t, &list, "incidents", "--key", "my-app")
	if len(list.Incidents) != 2 {
		t.Errorf("--json --key = %+v", list)
	}
}

func TestIncidentsSaysWhenThereAreNone(t *testing.T) {
	in := withOpenAI(t)
	if out := in.kman(t, "incidents"); !strings.Contains(out, "No incidents") {
		t.Errorf("output:\n%s", out)
	}
	if r := in.run(t, nil, "", "incidents", "--limit", "0"); r.code != 1 {
		t.Errorf("--limit 0: exit %d", r.code)
	}
	if r := in.run(t, nil, "", "incidents", "--key", "nope"); r.code != 1 || !strings.Contains(r.stderr, "nope") {
		t.Errorf("--key nope: exit %d, %s", r.code, r.stderr)
	}
}

func TestIncidentShowGivesTheTimelineAndWithholdsTheAddress(t *testing.T) {
	in, k := withIncident(t)
	in.detect(t, k.ID, "new_asn", store.SeverityHigh, store.Evidence{ASN: 3320})
	in.kman(t, "incident", "ack", "1")
	out := in.kman(t, "incident", "show", "1")
	for _, want := range []string{"incident:", "my-app (" + k.ID + ")", "acknowledged", "high", "new_country, new_asn", "Deutsche Telekom AG", "curl/8", "DE",
		"opened by system:detect", "acknowledged by owner", "kman settings set expose_client_addresses", "this-was-me"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "203.0.113") {
		t.Errorf("show gave out the address:\n%s", out)
	}
	// The timeline is in order: detection, escalation, acknowledgement.
	if i, j := strings.Index(out, "opened by system:detect"), strings.Index(out, "acknowledged by owner"); i < 0 || j < i {
		t.Errorf("the timeline is out of order:\n%s", out)
	}
	in.kman(t, "settings", "set", "expose_client_addresses", "true")
	if out := in.kman(t, "incident", "show", "1"); !strings.Contains(out, "203.0.113.7") || strings.Contains(out, "kman settings set expose_client_addresses") {
		t.Errorf("with the setting on:\n%s", out)
	}
	var d admin.IncidentDetail
	in.kmanJSON(t, &d, "incident", "show", "1")
	if d.ID != 1 || len(d.Detections) != 2 {
		t.Errorf("--json = %+v", d)
	}
}

func TestIncidentShowCleansWhatCameFromTheServer(t *testing.T) {
	in, k := withIncident(t)
	in.detect(t, k.ID, "new_client", store.SeverityHigh, store.Evidence{Client: "evil\x1b[31m/1", Detail: "a\x07b"})
	out := in.kman(t, "incident", "show", "1")
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("show printed a control character:\n%q", out)
	}
}

func TestIncidentAckAndResolve(t *testing.T) {
	in, _ := withIncident(t)
	if out := in.kman(t, "incident", "ack", "1"); !strings.Contains(out, "Acknowledged incident 1") {
		t.Errorf("ack:\n%s", out)
	}
	out := in.kman(t, "incident", "resolve", "1", "--as", "false-positive")
	if !strings.Contains(out, "Resolved incident 1 as a false positive") {
		t.Errorf("resolve:\n%s", out)
	}
	if r := in.run(t, nil, "", "incident", "resolve", "1", "--as", "confirmed"); r.code != 1 || !strings.Contains(r.stderr, "already resolved") {
		t.Errorf("resolving another way: exit %d, %s", r.code, r.stderr)
	}
	for _, args := range [][]string{{"incident", "ack", "x"}, {"incident", "ack"}, {"incident", "show", "0"}, {"incident", "frob", "1"}, {"incident"}} {
		if r := in.run(t, nil, "", args...); r.code != 1 {
			t.Errorf("kman %s: exit %d", strings.Join(args, " "), r.code)
		}
	}
	if r := in.run(t, nil, "", "incident", "show", "99"); r.code != 1 || !strings.Contains(r.stderr, "incident 99 does not exist") {
		t.Errorf("an unknown incident: exit %d, %s", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "incident", "resolve", "1", "--as", "expired"); r.code != 1 {
		t.Errorf("resolve as expired: exit %d", r.code)
	}
}

func TestIncidentResolveDefaultsToConfirmed(t *testing.T) {
	in, _ := withIncident(t)
	var got admin.Incident
	in.kmanJSON(t, &got, "incident", "resolve", "1")
	if got.Resolution != "confirmed" {
		t.Errorf("resolution = %q", got.Resolution)
	}
}

func TestThisWasMeResolvesAsAFalsePositiveAndHintsAtKnownSourcesWhenNotInteractive(t *testing.T) {
	in, _ := withIncident(t)
	out := in.kman(t, "incident", "this-was-me", "1")
	if !strings.Contains(out, "false positive") || !strings.Contains(out, "--add-known-sources") {
		t.Errorf("output:\n%s", out)
	}
	if ks := in.kman(t, "known-source", "list"); !strings.Contains(ks, "No known sources") {
		t.Errorf("known sources after a plain this-was-me:\n%s", ks)
	}
	// The hint works on the incident that is already a false positive.
	out = in.kman(t, "incident", "this-was-me", "1", "--add-known-sources")
	if !strings.Contains(out, "country DE") || !strings.Contains(out, "asn 3320") || !strings.Contains(out, "client curl/8") {
		t.Errorf("output:\n%s", out)
	}
}

func TestThisWasMeAddsKnownSourcesByFlagForTheKeyOrInstallWide(t *testing.T) {
	in, k := withIncident(t)
	in.kman(t, "settings", "set", "expose_client_addresses", "true")
	out := in.kman(t, "incident", "this-was-me", "1", "--add-known-sources", "--label", "the Berlin office")
	for _, want := range []string{"country DE", "cidr 203.0.113.0/24", "for key my-app", "the Berlin office"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	var list admin.KnownSourceList
	in.kmanJSON(t, &list, "known-source", "list", "--key", k.ID)
	if len(list.KnownSources) != 4 || list.KnownSources[0].Scope != k.ID {
		t.Errorf("known sources = %+v", list)
	}

	in2, _ := withIncident(t)
	out = in2.kman(t, "incident", "this-was-me", "1", "--add-known-sources", "--install-wide", "--kinds", "country,asn")
	if !strings.Contains(out, "every key") || strings.Contains(out, "client") {
		t.Errorf("output:\n%s", out)
	}
	var wideList admin.KnownSourceList
	in2.kmanJSON(t, &wideList, "known-source", "list")
	if len(wideList.KnownSources) != 2 || wideList.KnownSources[0].Scope != "" {
		t.Errorf("known sources = %+v", wideList)
	}
}

func TestThisWasMeSaysWhenAddressesAreWithheldSoNoNetworkWasAdded(t *testing.T) {
	in, _ := withIncident(t)
	out := in.kman(t, "incident", "this-was-me", "1", "--add-known-sources")
	if !strings.Contains(out, "network was not added") || !strings.Contains(out, "expose_client_addresses") {
		t.Errorf("output:\n%s", out)
	}
}

func TestThisWasMeAsksOnATerminalAndOnlyAddsOnYes(t *testing.T) {
	for _, c := range []struct {
		answer string
		added  bool
	}{{"y\n", true}, {"yes\n", true}, {"n\n", false}, {"\n", false}, {"", false}, {"maybe\n", false}} {
		in, _ := withIncident(t)
		var stdout, stderr strings.Builder
		env := in.env(nil, c.answer, &stdout, &stderr)
		env.Interactive = true
		if code := cli.Run(context.Background(), []string{"incident", "this-was-me", "1"}, env); code != 0 {
			t.Fatalf("answer %q: exit %d, %s%s", c.answer, code, stdout.String(), stderr.String())
		}
		for _, want := range []string{"country DE", "asn 3320", "[y/N]"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("answer %q: the prompt lacks %q:\n%s", c.answer, want, stdout.String())
			}
		}
		var list admin.KnownSourceList
		in.kmanJSON(t, &list, "known-source", "list")
		if got := len(list.KnownSources) > 0; got != c.added {
			t.Errorf("answer %q: known sources added = %v, want %v", c.answer, got, c.added)
		}
		var inc admin.Incident
		in.kmanJSON(t, &inc, "incident", "resolve", "1", "--as", "false-positive")
		if inc.Resolution != "false_positive" {
			t.Errorf("answer %q: the incident was resolved as %q", c.answer, inc.Resolution)
		}
	}
}

func TestThisWasMeNeverAsksOnACanaryAndSaysWhy(t *testing.T) {
	in := withOpenAI(t)
	var c admin.CreatedCanary
	in.kmanJSON(t, &c, "canary", "create", "decoy", "--planted", "the wiki")
	in.detect(t, c.Canary.ID, "canary", store.SeverityCritical, store.Evidence{Country: "DE"})
	var stdout, stderr strings.Builder
	env := in.env(nil, "y\n", &stdout, &stderr)
	env.Interactive = true
	if code := cli.Run(context.Background(), []string{"incident", "this-was-me", "1"}, env); code != 0 {
		t.Fatalf("exit %d, %s%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "[y/N]") || !strings.Contains(stdout.String(), "canary") {
		t.Errorf("output:\n%s", stdout.String())
	}
	if r := in.run(t, nil, "", "incident", "this-was-me", "1", "--add-known-sources"); r.code != 1 || !strings.Contains(r.stderr, "canary") {
		t.Errorf("--add-known-sources on a canary: exit %d, %s", r.code, r.stderr)
	}
}

func TestThisWasMeOptionsNeedAddKnownSources(t *testing.T) {
	in, _ := withIncident(t)
	for _, args := range [][]string{{"--install-wide"}, {"--kinds", "country"}, {"--label", "x"}} {
		if r := in.run(t, nil, "", append([]string{"incident", "this-was-me", "1"}, args...)...); r.code != 1 || !strings.Contains(r.stderr, "--add-known-sources") {
			t.Errorf("%v: exit %d, %s", args, r.code, r.stderr)
		}
	}
}

func TestKnownSourceAddListAndRemove(t *testing.T) {
	in, k := withIncident(t)
	out := in.kman(t, "known-source", "add", "cidr", "203.0.113.0/24", "--label", "CI runners")
	if !strings.Contains(out, "Added known source") || !strings.Contains(out, "every key") {
		t.Errorf("add:\n%s", out)
	}
	in.kman(t, "known-source", "add", "asn", "AS3320", "--label", "Telekom", "--key", "my-app")
	out = in.kman(t, "known-source", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[1], "CI runners") || !strings.Contains(lines[1], "every key") ||
		!strings.Contains(lines[2], "my-app") || !strings.Contains(lines[2], "3320") {
		t.Errorf("list:\n%s", out)
	}
	var list admin.KnownSourceList
	in.kmanJSON(t, &list, "known-source", "list", "--key", k.ID)
	if len(list.KnownSources) != 2 {
		t.Errorf("list --key = %+v", list)
	}
	if out := in.kman(t, "known-source", "rm", "1"); !strings.Contains(out, "Removed known source 1") {
		t.Errorf("rm:\n%s", out)
	}
	if r := in.run(t, nil, "", "known-source", "rm", "1"); r.code != 1 || !strings.Contains(r.stderr, "does not exist") {
		t.Errorf("rm twice: exit %d, %s", r.code, r.stderr)
	}
	for _, args := range [][]string{
		{"known-source", "add", "cidr", "0.0.0.0/0", "--label", "all"}, {"known-source", "add", "country", "DE"},
		{"known-source", "add", "country"}, {"known-source", "add", "planet", "x", "--label", "x"}, {"known-source"},
		{"known-source", "rm"}, {"known-source", "rm", "x"}, {"known-source", "add", "country", "DE", "--label", "x", "--key", "nope"},
	} {
		if r := in.run(t, nil, "", args...); r.code != 1 {
			t.Errorf("kman %s: exit %d", strings.Join(args, " "), r.code)
		}
	}
	if out := in.kman(t, "known-source", "list", "--key", "my-app"); strings.Contains(out, "No known") {
		t.Errorf("list --key by name:\n%s", out)
	}
}

func TestContainmentCommandsTakeAnIncident(t *testing.T) {
	t.Run("disable leaves it open", func(t *testing.T) {
		in, _ := withIncident(t)
		in.kman(t, "key", "disable", "my-app", "--incident", "1")
		var d admin.IncidentDetail
		in.kmanJSON(t, &d, "incident", "show", "1")
		if d.State != "open" || d.Actions[len(d.Actions)-1].Action != "key.disable" {
			t.Errorf("incident = %+v", d.Incident)
		}
	})
	t.Run("rotate resolves it as confirmed", func(t *testing.T) {
		in, _ := withIncident(t)
		out := in.kman(t, "key", "rotate", "my-app", "--incident", "1")
		if !strings.Contains(out, "incident 1") {
			t.Errorf("output does not say the incident was resolved:\n%s", out)
		}
		var inc admin.Incident
		in.kmanJSON(t, &inc, "incident", "resolve", "1")
		if inc.Resolution != "confirmed" {
			t.Errorf("incident = %+v", inc)
		}
	})
	t.Run("revoke resolves it as confirmed", func(t *testing.T) {
		in, _ := withIncident(t)
		in.kman(t, "key", "revoke", "my-app", "--incident", "1")
		if out := in.kman(t, "incidents", "--open"); !strings.Contains(out, "No incidents") {
			t.Errorf("open incidents after a revoke:\n%s", out)
		}
	})
	t.Run("compromise resolves it as confirmed", func(t *testing.T) {
		in, _ := withIncident(t)
		r := in.run(t, map[string]string{"NEW_CRED": "sk-live-replacement-0123456789abcdefQRST"}, "", "credential", "compromise", "openai", "--credential-env", "NEW_CRED", "--incident", "1")
		if r.code != 0 || !strings.Contains(r.stdout, "incident 1") {
			t.Fatalf("exit %d, %s%s", r.code, r.stdout, r.stderr)
		}
		if out := in.kman(t, "incidents", "--open"); !strings.Contains(out, "No incidents") {
			t.Errorf("open incidents after a compromise:\n%s", out)
		}
	})
	t.Run("a bad incident is refused and nothing changes", func(t *testing.T) {
		in, _ := withIncident(t)
		if r := in.run(t, nil, "", "key", "disable", "my-app", "--incident", "99"); r.code != 1 || !strings.Contains(r.stderr, "incident 99 does not exist") {
			t.Errorf("exit %d, %s", r.code, r.stderr)
		}
		if r := in.run(t, nil, "", "key", "rotate", "my-app", "--incident", "x"); r.code != 1 {
			t.Errorf("a non-numeric incident: exit %d", r.code)
		}
		if out := in.kman(t, "key", "show", "my-app"); !strings.Contains(out, "active") {
			t.Errorf("the key changed:\n%s", out)
		}
		// enable has no incident to name.
		if r := in.run(t, nil, "", "key", "enable", "my-app", "--incident", "1"); r.code != 1 {
			t.Errorf("key enable --incident: exit %d", r.code)
		}
	})
}

func TestIncidentsFollowPrintsRecentOnesThenEachChange(t *testing.T) {
	in, k := withIncident(t)
	ctx, cancel := context.WithCancel(context.Background())
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	env := in.env(nil, "", stdout, stderr)
	env.FollowEvery = time.Millisecond
	done := make(chan int)
	go func() { done <- cli.Run(ctx, []string{"incidents", "--follow"}, env) }()
	defer func() { cancel(); <-done }()
	wait := func(want string) string {
		t.Helper()
		start := time.Now()
		for !strings.Contains(stdout.String(), want) {
			if time.Since(start) > 10*time.Second {
				t.Fatalf("did not print %q:\n%s%s", want, stdout.String(), stderr.String())
			}
			runtime.Gosched()
		}
		return stdout.String()
	}
	wait("new_country")
	in.detect(t, k.ID, "new_asn", store.SeverityHigh, store.Evidence{})
	wait("new_country, new_asn")
	in.kman(t, "incident", "ack", "1")
	wait("acknowledged")
	in.kman(t, "incident", "resolve", "1")
	out := wait("resolved (confirmed)")
	if strings.Count(out, "#1") < 4 {
		t.Errorf("follow printed:\n%s", out)
	}
	in.detect(t, k.ID, "new_client", store.SeverityMedium, store.Evidence{})
	wait("#2")
}

// A quiet incident that fires again is an escalation, and alerts, though its
// state, severity and rules stay the same; --follow says so too. A repeat that
// is not after a quiet hour is not news (#111).
func TestIncidentsFollowPrintsAQuietIncidentFiringAgainButNotARepeat(t *testing.T) {
	in, k := withIncident(t)
	ctx, cancel := context.WithCancel(context.Background())
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	env := in.env(nil, "", stdout, stderr)
	env.FollowEvery = time.Millisecond
	done := make(chan int)
	go func() { done <- cli.Run(ctx, []string{"incidents", "--follow"}, env) }()
	defer func() { cancel(); <-done }()
	wait := func(want string, n int) string {
		t.Helper()
		start := time.Now()
		for strings.Count(stdout.String(), want) < n {
			if time.Since(start) > 10*time.Second {
				t.Fatalf("did not print %q %d times:\n%s%s", want, n, stdout.String(), stderr.String())
			}
			runtime.Gosched()
		}
		return stdout.String()
	}
	wait("#1", 1)
	ev := store.Evidence{Country: "DE"}
	if out := in.detectAt(t, t0.Add(10*time.Minute), k.ID, "new_country", store.SeverityHigh, ev); out.Change != detect.Repeated {
		t.Fatalf("a firing 10 minutes on is %s, want a repeat", out.Change)
	}
	// Another key's new incident is news: once it is printed, a poll has seen
	// the repeat, and printed nothing for it.
	var other admin.CreatedKey
	in.kmanJSON(t, &other, "key", "create", "other-app", "--upstream", "openai")
	in.detectAt(t, t0.Add(10*time.Minute), other.Key.ID, "new_country", store.SeverityMedium, ev)
	if out := wait("#2", 1); strings.Contains(out, "(2 detections)") {
		t.Errorf("a repeat was printed:\n%s", out)
	}
	if out := in.detectAt(t, t0.Add(2*time.Hour), k.ID, "new_country", store.SeverityHigh, ev); out.Change != detect.Escalated {
		t.Fatalf("a firing after two quiet hours is %s, want an escalation", out.Change)
	}
	if out := wait("#1", 2); !strings.Contains(out, "(3 detections)") {
		t.Errorf("follow printed:\n%s", out)
	}
}

func TestIncidentsFollowWithOpenStillTellsWhenOneIsResolved(t *testing.T) {
	in, _ := withIncident(t)
	ctx, cancel := context.WithCancel(context.Background())
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	env := in.env(nil, "", stdout, stderr)
	env.FollowEvery = time.Millisecond
	done := make(chan int)
	go func() { done <- cli.Run(ctx, []string{"incidents", "--follow", "--open", "--json"}, env) }()
	defer func() { cancel(); <-done }()
	waitFor := func(want string) {
		t.Helper()
		start := time.Now()
		for !strings.Contains(stdout.String(), want) {
			if time.Since(start) > 10*time.Second {
				t.Fatalf("did not print %q:\n%s%s", want, stdout.String(), stderr.String())
			}
			runtime.Gosched()
		}
	}
	waitFor(`"state":"open"`)
	in.kman(t, "incident", "resolve", "1")
	waitFor(`"state":"resolved"`)
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var i admin.Incident
		if err := json.Unmarshal([]byte(line), &i); err != nil || i.ID != 1 {
			t.Errorf("line %q: %v", line, err)
		}
	}
}

func TestIncidentsFollowEndsWithAnErrorWhenTheServerIsGone(t *testing.T) {
	in, _ := withIncident(t)
	in.sqlExec(t, `SELECT 1`) // the store is fine; the point is the login below
	r := in.run(t, map[string]string{"KMAN_ADMIN_URL": "http://127.0.0.1:1", "KMAN_ADMIN_TOKEN": in.owner}, "", "incidents", "--follow")
	if r.code != 1 || !strings.Contains(r.stderr, "cannot reach kman") {
		t.Errorf("exit %d, %s", r.code, r.stderr)
	}
}

// Declining the prompt with options given still resolves: the options belong to
// adding the sources, which was declined.
func TestThisWasMeDecliningWithOptionsStillResolves(t *testing.T) {
	in, _ := withIncident(t)
	var stdout, stderr strings.Builder
	env := in.env(nil, "n\n", &stdout, &stderr)
	env.Interactive = true
	args := []string{"incident", "this-was-me", "1", "--install-wide", "--kinds", "country", "--label", "home"}
	if code := cli.Run(context.Background(), args, env); code != 0 {
		t.Fatalf("exit %d, %s%s", code, stdout.String(), stderr.String())
	}
	var inc admin.Incident
	in.kmanJSON(t, &inc, "incident", "show", "1", "--json")
	if inc.Resolution != "false_positive" {
		t.Errorf("the incident was resolved as %q, want false_positive", inc.Resolution)
	}
}

// --follow starts after the incidents the wider window already holds: an older
// incident past --limit is not news two seconds in.
func TestIncidentsFollowDoesNotReprintIncidentsPastTheLimit(t *testing.T) {
	in, k := withIncident(t)
	in.detect(t, k.ID, "new_asn", store.SeverityHigh, store.Evidence{})
	var second admin.CreatedKey
	in.kmanJSON(t, &second, "key", "create", "second", "--upstream", "openai")
	in.detect(t, second.Key.ID, "new_client", store.SeverityMedium, store.Evidence{})
	ctx, cancel := context.WithCancel(context.Background())
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	env := in.env(nil, "", stdout, stderr)
	env.FollowEvery = time.Millisecond
	done := make(chan int)
	go func() { done <- cli.Run(ctx, []string{"incidents", "--follow", "--limit", "1"}, env) }()
	start := time.Now()
	for !strings.Contains(stdout.String(), "#2") && time.Since(start) < 10*time.Second {
		runtime.Gosched()
	}
	// A change to #2 is only printed by a poll after the first listing, which has
	// looked at #1 too by then.
	in.kman(t, "incident", "ack", "2")
	for !strings.Contains(stdout.String(), "acknowledged") && time.Since(start) < 10*time.Second {
		runtime.Gosched()
	}
	cancel()
	<-done
	if strings.Contains(stdout.String(), "#1") {
		t.Errorf("follow reprinted an older incident as a change:\n%s", stdout.String())
	}
}

// #89: the timeline says what was new, what the key normally does, and, with
// addresses exposed, the networks it normally uses.
func TestIncidentShowSaysWhatWasNewAndWhatIsUsual(t *testing.T) {
	in, k := withIncident(t)
	in.detect(t, k.ID, "new_network", store.SeverityLow, store.Evidence{Address: "198.51.100.9", Network: "198.51.100.0/24",
		Observed: "new network", Baseline: "1 usual network", Usual: "203.0.113.0/24 (40 requests)", Detail: "the request was refused (address_not_allowed)"})
	out := in.kman(t, "incident", "show", "1")
	for _, want := range []string{"observed new network, baseline 1 usual network", "the request was refused (address_not_allowed)"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "203.0.113.0/24") || strings.Contains(out, "198.51.100") {
		t.Errorf("show gave out a network while addresses are withheld:\n%s", out)
	}
	in.kman(t, "settings", "set", "expose_client_addresses", "true")
	if out := in.kman(t, "incident", "show", "1"); !strings.Contains(out, "usual networks 203.0.113.0/24 (40 requests)") {
		t.Errorf("with the setting on, show lacks the usual networks:\n%s", out)
	}
}
