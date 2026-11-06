package admin_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/registry"
)

func TestEveryCommandIsAuditedWithItsActor(t *testing.T) {
	in := newInstall(t)
	ctx := as("owner")
	if _, err := in.svc.CreateAdminToken(ctx, admin.CreateAdminTokenRequest{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RotateAdminToken(ctx, admin.AdminTokenName{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	tz := "Europe/Berlin"
	if _, err := in.svc.UpdateSettings(as("ci"), admin.SettingsUpdate{SpendTimezone: &tz}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RevokeAdminToken(ctx, admin.AdminTokenName{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	entries := in.latest(t, 10)
	var got []string
	for _, e := range entries {
		got = append(got, summary(e))
		if e.ClientIP != "127.0.0.1" || !e.Time.Equal(t0) {
			t.Errorf("entry %s has client IP %q and time %v", summary(e), e.ClientIP, e.Time)
		}
		if strings.Contains(string(e.Before)+string(e.After), "kma_") {
			t.Errorf("entry %s holds a secret: %s %s", summary(e), e.Before, e.After)
		}
	}
	want := []string{
		"system:init admin_token.create admin_token/owner",
		"owner admin_token.create admin_token/ci",
		"owner admin_token.rotate admin_token/ci",
		"ci settings.set setting/spend_timezone",
		"owner admin_token.revoke admin_token/ci",
	}
	if !slices.Equal(got, want) {
		t.Errorf("audit log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if s := entries[3]; string(s.Before) != `"UTC"` || string(s.After) != `"Europe/Berlin"` {
		t.Errorf("the settings entry records %s -> %s, want \"UTC\" -> \"Europe/Berlin\"", s.Before, s.After)
	}
}

func TestACommandWithoutACallerIsRefused(t *testing.T) {
	in := newInstall(t)
	before := len(in.latest(t, 100))
	if _, err := in.svc.CreateAdminToken(context.Background(), admin.CreateAdminTokenRequest{Name: "ci"}); code(err) != admin.CodeUnauthorized {
		t.Errorf("CreateAdminToken with no caller = %v, want unauthorized", err)
	}
	if _, err := in.svc.ListAdminTokens(context.Background()); code(err) != admin.CodeUnauthorized {
		t.Errorf("ListAdminTokens with no caller = %v, want unauthorized", err)
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("the audit log grew from %d to %d entries", before, after)
	}
}

func TestAFailedCommandChangesNothingAndIsNotPublished(t *testing.T) {
	in := newInstall(t)
	before, applied := len(in.latest(t, 100)), len(in.applied)
	if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "owner"}); code(err) != admin.CodeConflict {
		t.Fatalf("a second owner = %v, want conflict", err)
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("a failed command wrote %d audit entries", after-before)
	}
	if len(in.applied) != applied {
		t.Errorf("a failed command was published to the registry: %+v", in.applied[applied:])
	}
}

func TestACommandIsPublishedAfterItCommits(t *testing.T) {
	st := newInstall(t).store
	var seen []string
	var svc *admin.Service
	svc = admin.New(admin.Options{
		Store: st,
		Now:   func() time.Time { return t0 },
		Apply: func(c registry.Change) {
			// The store has one connection, so this read would wait for
			// ever inside the command's transaction: it must have committed.
			ctx, cancel := context.WithTimeout(as("owner"), 5*time.Second)
			defer cancel()
			list, err := svc.ListAdminTokens(ctx)
			if err != nil {
				t.Errorf("reading the store from Apply: %v", err)
				return
			}
			for _, tok := range list.Tokens {
				seen = append(seen, tok.Name)
			}
			if c.Generation == 0 {
				t.Errorf("Apply got %+v, want a generation", c)
			}
		},
	})
	if _, err := svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(seen, "ci") {
		t.Errorf("Apply saw tokens %v, want ci among them", seen)
	}
}

func TestAuditPagesForwardFromACursor(t *testing.T) {
	in := newInstall(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	// Five entries: owner, then a to d.
	var ids []string
	var after int64
	for range 10 {
		page, err := in.svc.Audit(as("owner"), admin.AuditQuery{After: after, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Entries {
			ids = append(ids, e.TargetID)
			after = e.ID
		}
		if !page.More {
			break
		}
	}
	if want := []string{"owner", "a", "b", "c", "d"}; !slices.Equal(ids, want) {
		t.Errorf("paged through %v, want %v", ids, want)
	}
	latest, err := in.svc.Audit(as("owner"), admin.AuditQuery{Latest: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Entries) != 2 || latest.Entries[1].TargetID != "d" || !latest.More {
		t.Errorf("the latest two = %+v, want c and d with more before them", latest)
	}
	for _, q := range []admin.AuditQuery{{Limit: -1}, {Limit: 1001}, {After: -1}, {Latest: true, After: 3}} {
		if _, err := in.svc.Audit(as("owner"), q); code(err) != admin.CodeInvalid {
			t.Errorf("Audit(%+v) = %v, want invalid", q, err)
		}
	}
}

func TestWhoamiNamesTheCaller(t *testing.T) {
	in := newInstall(t)
	w, err := in.svc.Whoami(as("owner"))
	if err != nil || w.Actor != "owner" {
		t.Errorf("Whoami = %+v, %v; want owner", w, err)
	}
}
