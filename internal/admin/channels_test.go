package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

const slackURL = "https://hooks.slack.test/services/T000/B000/XXXXsecrettoken"

// fakeAlerts stands for the alert service: it records what admin hands it.
type fakeAlerts struct {
	mu      sync.Mutex
	audited []store.AuditEntry
	inTx    []bool // whether the audit entry was already in the transaction
	wakes   int
	tested  []string
	testErr error
	fail    error
}

func (f *fakeAlerts) AlertAudit(tx *store.Tx, e store.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	got, err := tx.AuditEntries(e.ID-1, 1)
	f.inTx = append(f.inTx, err == nil && len(got) == 1 && got[0].ID == e.ID)
	f.audited = append(f.audited, e)
	return f.fail
}
func (f *fakeAlerts) Wake() { f.mu.Lock(); f.wakes++; f.mu.Unlock() }
func (f *fakeAlerts) Test(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, id)
	return f.testErr
}

func withAlerts(t *testing.T) (*install, *fakeAlerts) {
	t.Helper()
	in := newInstall(t)
	f := &fakeAlerts{}
	in.alerts = f
	in.svc = in.service(nil)
	return in, f
}

func addSlack(t *testing.T, in *install, name string) admin.CreatedChannel {
	t.Helper()
	got, err := in.svc.AddChannel(as("owner"), admin.AddChannelRequest{Name: name, Kind: "slack", URL: slackURL})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestASlackChannelIsSealedAndCachedAndShowsOnlyItsHost(t *testing.T) {
	in, _ := withAlerts(t)
	got := addSlack(t, in, "ops")
	c := got.Channel
	if !strings.HasPrefix(c.ID, "ch_") || c.Name != "ops" || c.Kind != "slack" || c.Host != "hooks.slack.test" ||
		c.MinSeverity != "medium" || !c.LifecycleAlerts || !c.CreatedAt.Equal(t0) {
		t.Errorf("AddChannel = %+v", c)
	}
	if got.SigningSecret != "" {
		t.Errorf("a Slack channel has a signing secret: %q", got.SigningSecret)
	}
	plain, ok := in.vault.Credential(c.ID)
	var cfg store.AlertChannelConfig
	if !ok || json.Unmarshal(plain, &cfg) != nil || cfg.URL != slackURL {
		t.Errorf("the vault holds %q, %v; want the address", plain, ok)
	}
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		row, err := tx.AlertChannel(c.ID)
		if err != nil {
			return err
		}
		if strings.Contains(string(row.ConfigCiphertext), "XXXXsecrettoken") || len(row.ConfigCiphertext) == 0 ||
			row.DataKeyID != in.vault.DataKeyID() || row.DisplayHost != "hooks.slack.test" {
			t.Errorf("the row = %+v, want ciphertext under the data key and the host to show", row)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAWebhookChannelGetsASigningSecretShownOnce(t *testing.T) {
	in, _ := withAlerts(t)
	got, err := in.svc.AddChannel(as("owner"), admin.AddChannelRequest{Name: "pager", Kind: "webhook", URL: "https://pager.example.test/in", MinSeverity: "high"})
	if err != nil {
		t.Fatal(err)
	}
	secret := string(got.SigningSecret)
	if !strings.HasPrefix(secret, "kmwhsec_") || len(secret) < 40 {
		t.Fatalf("signing secret = %q, want kmwhsec_ and 32 random bytes", secret)
	}
	plain, _ := in.vault.Credential(got.Channel.ID)
	var cfg store.AlertChannelConfig
	if json.Unmarshal(plain, &cfg) != nil || cfg.SigningSecret != secret {
		t.Errorf("the vault holds %q, want the signing secret to sign with", plain)
	}
	list, err := in.svc.ListChannels(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	listed, _ := json.Marshal(list)
	if strings.Contains(string(listed), secret) || strings.Contains(string(listed), "pager.example.test/in") {
		t.Errorf("the list shows the secret or the address: %s", listed)
	}
	for _, e := range in.latest(t, 5) {
		if strings.Contains(e.TargetID+string(e.Before)+string(e.After), secret) || strings.Contains(string(e.After), "/in") {
			t.Errorf("the audit entry holds a secret or the address: %+v", e)
		}
	}
}

func TestAddingAChannelIsAudited(t *testing.T) {
	in, _ := withAlerts(t)
	c := addSlack(t, in, "ops").Channel
	last := in.latest(t, 1)[0]
	if summary(last) != "owner alert_channel.create alert_channel/"+c.ID {
		t.Errorf("audit = %s", summary(last))
	}
	if strings.Contains(string(last.After), "XXXX") || strings.Contains(string(last.After), "hooks.slack.test/services") {
		t.Errorf("the entry holds the address: %s", last.After)
	}
}

func TestAChannelRequestThatBreaksARuleIsRefused(t *testing.T) {
	in, _ := withAlerts(t)
	addSlack(t, in, "ops")
	tru, fls := true, false
	cases := []struct {
		name string
		req  admin.AddChannelRequest
		code admin.Code
	}{
		{"a bad name", admin.AddChannelRequest{Name: "Ops Team", Kind: "slack", URL: slackURL}, admin.CodeInvalid},
		{"an unknown kind", admin.AddChannelRequest{Name: "x", Kind: "discord", URL: slackURL}, admin.CodeInvalid},
		{"no address", admin.AddChannelRequest{Name: "x", Kind: "slack"}, admin.CodeInvalid},
		{"an address that is not a URL", admin.AddChannelRequest{Name: "x", Kind: "slack", URL: "hooks.slack.test/x"}, admin.CodeInvalid},
		{"plain http to another host", admin.AddChannelRequest{Name: "x", Kind: "webhook", URL: "http://pager.example.test/in"}, admin.CodeInvalid},
		{"a login in the address", admin.AddChannelRequest{Name: "x", Kind: "webhook", URL: "https://user:pw@pager.example.test/in"}, admin.CodeInvalid},
		{"another scheme", admin.AddChannelRequest{Name: "x", Kind: "webhook", URL: "ftp://pager.example.test/in"}, admin.CodeInvalid},
		{"an unknown severity", admin.AddChannelRequest{Name: "x", Kind: "slack", URL: slackURL, MinSeverity: "urgent"}, admin.CodeInvalid},
		{"a name in use", admin.AddChannelRequest{Name: "ops", Kind: "slack", URL: slackURL}, admin.CodeConflict},
	}
	for _, c := range cases {
		if _, err := in.svc.AddChannel(as("owner"), c.req); code(err) != c.code {
			t.Errorf("%s: err = %v, want %s", c.name, err, c.code)
		}
	}
	// Plain http is fine to the machine's own loopback, for a local bridge.
	for _, url := range []string{"http://127.0.0.1:9000/in", "http://localhost:9000/in", "http://[::1]:9000/in"} {
		if _, err := in.svc.AddChannel(as("owner"), admin.AddChannelRequest{Name: "local" + strings.NewReplacer(":", "", "/", "", ".", "", "[", "", "]", "").Replace(url), Kind: "webhook", URL: admin.Secret(url)}); err != nil {
			t.Errorf("%s: %v", url, err)
		}
	}
	// And the toggle can be turned off, and a severity chosen.
	got, err := in.svc.AddChannel(as("owner"), admin.AddChannelRequest{Name: "quiet", Kind: "slack", URL: slackURL, MinSeverity: "critical", LifecycleAlerts: &fls})
	if err != nil || got.Channel.MinSeverity != "critical" || got.Channel.LifecycleAlerts {
		t.Errorf("AddChannel = %+v, %v", got.Channel, err)
	}
	if got, err := in.svc.AddChannel(as("owner"), admin.AddChannelRequest{Name: "loud", Kind: "slack", URL: slackURL, LifecycleAlerts: &tru}); err != nil || !got.Channel.LifecycleAlerts {
		t.Errorf("AddChannel = %+v, %v", got.Channel, err)
	}
	// A refused request left no audit entry for itself.
	for _, e := range in.latest(t, 50) {
		if e.Action == "alert_channel.create" && strings.Contains(string(e.After), "Ops Team") {
			t.Errorf("a refused request was audited: %+v", e)
		}
	}
}

func TestListChannelsShowsEachOnesOutbox(t *testing.T) {
	in, _ := withAlerts(t)
	ops := addSlack(t, in, "ops").Channel
	addSlack(t, in, "audit")
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		a, err := tx.EnqueueAlert(store.OutboxAlert{ChannelID: ops.ID, Kind: "audit", PayloadJSON: "{}", CreatedAt: t0})
		if err != nil {
			return err
		}
		if _, err := tx.EnqueueAlert(store.OutboxAlert{ChannelID: ops.ID, Kind: "audit", PayloadJSON: "{}", CreatedAt: t0}); err != nil {
			return err
		}
		return tx.AlertFailed(a.ID, t0, "the channel answered 404 Not Found")
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := in.svc.ListChannels(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Channels) != 2 || list.Channels[0].Name != "audit" || list.Channels[1].Name != "ops" {
		t.Fatalf("ListChannels = %+v, want audit then ops", list.Channels)
	}
	if got := list.Channels[1]; got.Pending < 1 || got.Failed != 1 || got.LastError != "the channel answered 404 Not Found" {
		t.Errorf("ops = %+v, want its outbox counts and last error", got)
	}
}

func TestRemovingAChannelWipesItsSecretAndIsAudited(t *testing.T) {
	in, _ := withAlerts(t)
	c := addSlack(t, in, "ops").Channel
	got, err := in.svc.RemoveChannel(as("owner"), admin.ChannelRef{Ref: "ops"})
	if err != nil || got.ID != c.ID {
		t.Fatalf("RemoveChannel = %+v, %v", got, err)
	}
	if _, ok := in.vault.Credential(c.ID); ok {
		t.Error("the vault still holds the removed channel's address")
	}
	if list, _ := in.svc.ListChannels(as("owner")); len(list.Channels) != 0 {
		t.Errorf("the removed channel is still listed: %+v", list.Channels)
	}
	if s := summary(in.latest(t, 1)[0]); s != "owner alert_channel.delete alert_channel/"+c.ID {
		t.Errorf("audit = %s", s)
	}
	if _, err := in.svc.RemoveChannel(as("owner"), admin.ChannelRef{Ref: c.ID}); code(err) != admin.CodeNotFound {
		t.Errorf("removing it again: %v, want not found", err)
	}
	addSlack(t, in, "ops") // its name is free again
}

func TestTestChannelReportsTheResultInsteadOfFailing(t *testing.T) {
	in, f := withAlerts(t)
	c := addSlack(t, in, "ops").Channel
	got, err := in.svc.TestChannel(as("owner"), admin.ChannelRef{Ref: "ops"})
	if err != nil || !got.OK || got.Channel != "ops" || got.Error != "" || len(f.tested) != 1 || f.tested[0] != c.ID {
		t.Fatalf("TestChannel = %+v, %v (tested %v)", got, err, f.tested)
	}
	f.testErr = errors.New("the channel answered 404 Not Found")
	got, err = in.svc.TestChannel(as("owner"), admin.ChannelRef{Ref: c.ID})
	if err != nil || got.OK || got.Error != "the channel answered 404 Not Found" {
		t.Errorf("TestChannel of a failing channel = %+v, %v, want a result that says why", got, err)
	}
	if _, err := in.svc.TestChannel(as("owner"), admin.ChannelRef{Ref: "nope"}); code(err) != admin.CodeNotFound {
		t.Errorf("TestChannel of no channel: %v, want not found", err)
	}
}

func TestEveryChannelCommandNeedsACaller(t *testing.T) {
	in, _ := withAlerts(t)
	ctx := t.Context()
	for name, err := range map[string]error{
		"add":    second(in.svc.AddChannel(ctx, admin.AddChannelRequest{Name: "x", Kind: "slack", URL: slackURL})),
		"list":   second(in.svc.ListChannels(ctx)),
		"test":   second(in.svc.TestChannel(ctx, admin.ChannelRef{Ref: "x"})),
		"remove": second(in.svc.RemoveChannel(ctx, admin.ChannelRef{Ref: "x"})),
	} {
		if code(err) != admin.CodeUnauthorized {
			t.Errorf("%s with no caller: %v, want unauthorized", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func TestEveryAuditEntryIsHandedToTheAlertsInItsTransactionAndWakesThemOnCommit(t *testing.T) {
	in, f := withAlerts(t)
	c := addSlack(t, in, "ops").Channel
	if _, err := in.svc.RemoveChannel(as("owner"), admin.ChannelRef{Ref: "ops"}); err != nil {
		t.Fatal(err)
	}
	if len(f.audited) != 2 || f.audited[0].Action != "alert_channel.create" || f.audited[1].Action != "alert_channel.delete" ||
		f.audited[0].TargetID != c.ID || f.audited[0].Actor != "owner" || f.audited[0].ID == 0 {
		t.Fatalf("alerts were handed %+v", f.audited)
	}
	if f.inTx[0] != true || f.inTx[1] != true {
		t.Errorf("the audit entry was not yet in the transaction when alerts saw it: %v", f.inTx)
	}
	if f.wakes < 2 {
		t.Errorf("alerts were woken %d times, want once per command", f.wakes)
	}
}

func TestACommandWhoseAlertCannotBeQueuedChangesNothing(t *testing.T) {
	in, f := withAlerts(t)
	f.fail = errors.New("the outbox is locked")
	_, err := in.svc.AddChannel(as("owner"), admin.AddChannelRequest{Name: "ops", Kind: "slack", URL: slackURL})
	if err == nil {
		t.Fatal("AddChannel succeeded with no way to queue its alert")
	}
	f.fail = nil
	if list, _ := in.svc.ListChannels(as("owner")); len(list.Channels) != 0 {
		t.Errorf("the failed command left %+v", list.Channels)
	}
	if f.wakes != 0 {
		t.Errorf("a command that rolled back woke the alerts %d times", f.wakes)
	}
}

func TestChannelsAreLoadedIntoTheVaultAtStartup(t *testing.T) {
	in, _ := withAlerts(t)
	c := addSlack(t, in, "ops").Channel
	in.vault.Delete(c.ID) // as after a restart: nothing cached yet
	if err := in.service(nil).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if plain, ok := in.vault.Credential(c.ID); !ok || !strings.Contains(string(plain), slackURL) {
		t.Errorf("after Load the vault holds %q, %v", plain, ok)
	}
}
