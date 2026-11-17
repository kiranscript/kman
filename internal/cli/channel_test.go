package cli_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

var regexpSigning = regexp.MustCompile(`kmwhsec_[0-9a-f]{64}`)

const hookURL = "https://hooks.slack.test/services/T000/B000/XXXXsecrettoken"

// alerter stands for the alert service: a test sends nothing anywhere.
type alerter struct{ err error }

func (alerter) AlertAudit(*store.Tx, store.AuditEntry) error { return nil }
func (alerter) Wake()                                        {}
func (a alerter) Test(context.Context, string) error         { return a.err }

func TestChannelAddAsksForTheAddressWithoutEchoAndNeverPrintsIt(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	r := in.kmanIn(t, hookURL+"\n", "channel", "add", "ops", "--kind", "slack")
	if strings.Contains(r.stdout+r.stderr, "XXXXsecrettoken") {
		t.Errorf("the address is printed:\n%s", r.stdout)
	}
	for _, want := range []string{"Added channel ops", "hooks.slack.test", "kman channel test ops"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	var list admin.ChannelList
	in.kmanJSON(t, &list, "channel", "list")
	if len(list.Channels) != 1 || list.Channels[0].MinSeverity != "medium" || !list.Channels[0].LifecycleAlerts {
		t.Errorf("list = %+v, want one channel with the defaults", list.Channels)
	}
}

func TestChannelAddReadsTheAddressFromTheNamedVariable(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	r := in.run(t, map[string]string{"SLACK_URL": hookURL}, "", "channel", "add", "ops", "--kind", "slack", "--url-env", "SLACK_URL",
		"--min-severity", "high", "--lifecycle-alerts=false")
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	var list admin.ChannelList
	in.kmanJSON(t, &list, "channel", "list")
	if c := list.Channels[0]; c.MinSeverity != "high" || c.LifecycleAlerts {
		t.Errorf("channel = %+v, want min_severity high and warnings off", c)
	}
}

func TestChannelAddForAWebhookPrintsTheSigningSecretOnce(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	r := in.kmanIn(t, "https://pager.example.test/in\n", "channel", "add", "pager", "--kind", "webhook")
	secret := regexpSigning.FindString(r.stdout)
	if secret == "" || !strings.Contains(r.stdout, "only this once") || !strings.Contains(r.stdout, "X-Kman-Signature") {
		t.Fatalf("output has no signing secret, a once-only warning and how to verify:\n%s", r.stdout)
	}
	if out := in.kman(t, "channel", "list"); strings.Contains(out, secret) {
		t.Errorf("list shows the signing secret:\n%s", out)
	}
}

func TestChannelListShowsEachChannelAndItsOutbox(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, hookURL+"\n", "channel", "add", "ops", "--kind", "slack")
	out := in.kman(t, "channel", "list")
	for _, want := range []string{"NAME", "KIND", "HOST", "MIN SEVERITY", "ops", "slack", "hooks.slack.test", "medium"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "XXXX") || strings.Contains(out, "services/T000") {
		t.Errorf("list shows the address:\n%s", out)
	}
	if empty := newInstall(t).loggedIn(t).kman(t, "channel", "list"); !strings.Contains(empty, "kman channel add") {
		t.Errorf("an install with no channel says how to add one:\n%s", empty)
	}
}

func TestChannelTestSaysWhetherTheAlertArrived(t *testing.T) {
	in := newInstall(t)
	in.alerts = alerter{}
	in.upgrade(t, nil)
	in.kmanIn(t, hookURL+"\n", "channel", "add", "ops", "--kind", "slack")
	if out := in.kman(t, "channel", "test", "ops"); !strings.Contains(out, "Test alert delivered to ops") {
		t.Errorf("output:\n%s", out)
	}
	in.alerts = alerter{err: errors.New("the channel answered 404 Not Found")}
	in.upgrade(t, nil)
	r := in.run(t, nil, "", "channel", "test", "ops")
	if r.code != 1 || !strings.Contains(r.stderr, "the channel answered 404 Not Found") || strings.Contains(r.stderr+r.stdout, "XXXX") {
		t.Errorf("a failing channel: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestChannelRmRemovesAChannel(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kmanIn(t, hookURL+"\n", "channel", "add", "ops", "--kind", "slack")
	if out := in.kman(t, "channel", "rm", "ops"); !strings.Contains(out, "Removed channel ops") {
		t.Errorf("output:\n%s", out)
	}
	var list admin.ChannelList
	in.kmanJSON(t, &list, "channel", "list")
	if len(list.Channels) != 0 {
		t.Errorf("list = %+v", list.Channels)
	}
}

func TestChannelRefusesWhatItCannotDo(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, tc := range []struct {
		args  []string
		stdin string
		want  string
	}{
		{[]string{"channel"}, "", "channel <add|list|test|rm>"},
		{[]string{"channel", "add", "ops"}, hookURL, "--kind"},
		{[]string{"channel", "add", "ops", "--kind", "discord"}, hookURL, "slack or webhook"},
		{[]string{"channel", "add", "ops", "--kind", "slack"}, "", "no address given"},
		{[]string{"channel", "add", "ops", "--kind", "slack", "--min-severity", "urgent"}, hookURL, "min_severity"},
		{[]string{"channel", "add", "ops", "--kind", "slack"}, "http://hooks.example.test/x", "https"},
		{[]string{"channel", "rm", "nobody"}, "", "nobody"},
		{[]string{"channel", "test", "nobody"}, "", "nobody"},
	} {
		r := in.run(t, nil, tc.stdin, tc.args...)
		if r.code == 0 || !strings.Contains(r.stderr, tc.want) {
			t.Errorf("kman %s: exit %d, stderr %q; want a failure mentioning %q", strings.Join(tc.args, " "), r.code, r.stderr, tc.want)
		}
		if strings.Contains(r.stderr+r.stdout, "hooks.example.test/x") || strings.Contains(r.stderr+r.stdout, "XXXX") {
			t.Errorf("kman %s printed the address:\n%s%s", strings.Join(tc.args, " "), r.stdout, r.stderr)
		}
	}
}
