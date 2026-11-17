package alerts_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/kirank55/kman/internal/store"
)

// withKey makes a key in the install's store, as admin would, so alerts can name it.
func (in *install) withKey(id, name string) {
	in.t.Helper()
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.CreateKey(store.VirtualKey{ID: id, Name: name, Kind: store.KeyCanary, CreatedAt: t0, PolicyJSON: "{}", TagsJSON: "[]"}, []byte("hash"))
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
}

// audited is what admin does: the audit entry and the alert for it commit together.
func (in *install) audited(e store.AuditEntry) {
	in.t.Helper()
	e.TS = t0
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		id, err := tx.AppendAudit(e)
		if err != nil {
			return err
		}
		e.ID = id
		return in.svc.AlertAudit(tx, e)
	})
	if err != nil {
		in.t.Fatal(err)
	}
	in.svc.Wake()
}

func TestTheAuditLogIsAnAlertProducerAndNamesTheKeyAndWhatToTypeNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.withKey("abcd1234", "ci-bot")
		stop := in.run()
		defer stop()
		in.audited(store.AuditEntry{Actor: "system:detect", Action: "key.disable", TargetKind: "key", TargetID: "abcd1234", ClientIP: "203.0.113.9"})
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1", len(got))
		}
		for _, want := range []string{"MEDIUM", "ci-bot", "abcd1234", "system:detect", "key.disable", "203.0.113.9",
			"kman key show abcd1234", "kman key enable abcd1234", "kman audit"} {
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("the alert lacks %q:\n%s", want, got[0].Body)
			}
		}
	})
}

func TestAnAuditAlertNeverCarriesWhatTheEntryRecordedOfTheChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", true, "")
		in.withKey("abcd1234", "ci-bot")
		stop := in.run()
		defer stop()
		in.audited(store.AuditEntry{Actor: "owner", Action: "key.update", TargetKind: "key", TargetID: "abcd1234",
			BeforeJSON: `{"note":"before-marker"}`, AfterJSON: `{"policy":"after-marker"}`})
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1", len(got))
		}
		if strings.Contains(got[0].Body, "before-marker") || strings.Contains(got[0].Body, "after-marker") {
			t.Errorf("the alert holds the entry's before or after:\n%s", got[0].Body)
		}
	})
}

func TestAuditActionsAreAlertedByHowMuchTheyMatter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_med", "slack", "med.test", "medium", true, "")
		in.channel("ch_crit", "slack", "crit.test", "critical", true, "")
		stop := in.run()
		defer stop()
		for _, action := range []string{"settings.set", "key.create", "key.disable", "admin_token.create", "credential.compromise", "data.prune", "backup.create", "something.new"} {
			in.audited(store.AuditEntry{Actor: "owner", Action: action, TargetKind: "x", TargetID: "y"})
		}
		synctest.Wait()
		titles := func(host string) string {
			var b strings.Builder
			for _, r := range in.net.received(host) {
				b.WriteString(r.Body)
			}
			return b.String()
		}
		med := titles("med.test")
		for _, want := range []string{"key.disable", "admin_token.create", "credential.compromise", "data.prune", "backup.create"} {
			if !strings.Contains(med, want) {
				t.Errorf("a medium channel missed %s", want)
			}
		}
		for _, not := range []string{"settings.set", "key.create", "something.new"} {
			if strings.Contains(med, not) {
				t.Errorf("a medium channel was told of %s, which is low", not)
			}
		}
		if got := len(in.net.received("crit.test")); got != 0 {
			t.Errorf("a critical channel got %d audit alerts, want none: nothing in the audit log is critical", got)
		}
	})
}
