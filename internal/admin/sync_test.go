package admin_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/registry"
)

// catalog is one preset, "acme", at version 1, and its next version: another
// host and auth header, one rule gone and one new.
func catalog() (v1, v2 []registry.Preset) {
	spec := registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.acme.test", Port: 443, TLS: true,
		Paths: []string{"GET /v1/a", "GET /v1/b"},
		Auth:  registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"},
	}
	next := spec
	next.Host = "api2.acme.test"
	next.Paths = []string{"GET /v1/a", "POST /v1/c"}
	next.Auth.Header = "X-Acme-Key: {key}"
	next.SDKEnv = "ACME_KEY"
	return []registry.Preset{{Name: "acme", Title: "Acme", Version: 1, Spec: spec}},
		[]registry.Preset{{Name: "acme", Title: "Acme", Version: 2, Spec: next}}
}

// upgraded is an install with an upstream made from acme v1 and a credential on
// it, and a service as the next release of kman would run it.
func upgraded(t *testing.T) (*install, *admin.Service) {
	t.Helper()
	v1, v2 := catalog()
	in := newInstallWith(t, v1)
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "acme", Preset: "acme"}); err != nil {
		t.Fatal(err)
	}
	in.addCredential(t, "main", "acme")
	return in, in.service(v2)
}

func TestAnUpgradeNeverChangesAnExistingUpstream(t *testing.T) {
	_, next := upgraded(t)
	u, err := next.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "api.acme.test" || u.PresetVersion != 1 {
		t.Errorf("after the upgrade: %s at version %d", u.Host, u.PresetVersion)
	}
	if !u.UpdateAvailable {
		t.Error("the upstream does not say a newer preset exists")
	}
}

func TestSyncPlanShowsHowTheUpstreamDiffersFromTheNewerPreset(t *testing.T) {
	_, next := upgraded(t)
	plan, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.UpToDate || plan.Preset != "acme" || plan.FromVersion != 1 || plan.ToVersion != 2 {
		t.Errorf("plan = %+v", plan)
	}
	want := []admin.UpstreamChange{
		{Field: "host", From: "api.acme.test", To: "api2.acme.test"},
		{Field: "path_allowlist", From: "GET /v1/b"},
		{Field: "path_allowlist", To: "POST /v1/c"},
		{Field: "auth", From: "Authorization: Bearer {key}", To: "X-Acme-Key: {key}"},
		{Field: "sdk_env", To: "ACME_KEY"},
	}
	if !slices.Equal(plan.Changes, want) {
		t.Errorf("changes = %+v, want %+v", plan.Changes, want)
	}
}

func TestPlanningASyncChangesNothing(t *testing.T) {
	in, next := upgraded(t)
	before, generation := len(in.latest(t, 100)), in.reg.Generation()
	if _, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"}); err != nil {
		t.Fatal(err)
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("the audit log grew by %d", after-before)
	}
	if g := in.reg.Generation(); g != generation {
		t.Errorf("the registry moved to generation %d", g)
	}
}

func TestSyncAppliesThePlanAndRecordsTheVersion(t *testing.T) {
	in, next := upgraded(t)
	plan, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := next.SyncUpstream(as("ci"), admin.UpstreamSyncRequest{Ref: "acme", ToVersion: plan.ToVersion, Generation: plan.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "api2.acme.test" || got.PresetVersion != 2 || got.UpdateAvailable || got.Auth.Header != "X-Acme-Key: {key}" ||
		!slices.Equal(got.Paths, []string{"GET /v1/a", "POST /v1/c"}) || got.SDKEnv != "ACME_KEY" {
		t.Errorf("SyncUpstream = %+v", got)
	}
	if len(got.Credentials) != 1 || got.Credentials[0].Name != "main" {
		t.Errorf("it lost its credentials: %+v", got.Credentials)
	}
	e := in.latest(t, 1)[0]
	if want := "ci upstream.sync upstream/" + got.ID; summary(e) != want {
		t.Errorf("audit = %s, want %s", summary(e), want)
	}
	if !strings.Contains(string(e.Before), `"preset_version":1`) || !strings.Contains(string(e.After), `"preset_version":2`) {
		t.Errorf("the entry records %s -> %s", e.Before, e.After)
	}
	if u, _ := in.reg.Upstream(got.ID); u.Host != "api2.acme.test" || u.PresetVersion != 2 {
		t.Errorf("the registry holds %+v", u)
	}
	after, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil || !after.UpToDate || len(after.Changes) != 0 {
		t.Errorf("the next plan = %+v, %v; want up to date", after, err)
	}
}

// Sync copies the preset, which is what the plan showed: the operator's edit
// goes, and the plan said so. Its name stays.
func TestSyncReplacesAnEditThePlanShowed(t *testing.T) {
	in, next := upgraded(t)
	if _, err := in.svc.UpdateUpstream(as("owner"), admin.UpstreamUpdate{Ref: "acme", Name: ptr("renamed"),
		UpstreamSettings: admin.UpstreamSettings{Port: ptr(8443)}}); err != nil {
		t.Fatal(err)
	}
	plan, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Changes, admin.UpstreamChange{Field: "port", From: "8443", To: "443"}) {
		t.Errorf("changes = %+v, want the port among them", plan.Changes)
	}
	got, err := next.SyncUpstream(as("owner"), admin.UpstreamSyncRequest{Ref: "renamed", ToVersion: 2, Generation: plan.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 443 || got.Name != "renamed" {
		t.Errorf("got port %d, name %s", got.Port, got.Name)
	}
}

func TestAnUpstreamChangedSinceThePlanIsNotSynced(t *testing.T) {
	in, next := upgraded(t)
	plan, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.UpdateUpstream(as("owner"), admin.UpstreamUpdate{Ref: "acme",
		UpstreamSettings: admin.UpstreamSettings{Port: ptr(8443)}}); err != nil {
		t.Fatal(err)
	}
	_, err = next.SyncUpstream(as("owner"), admin.UpstreamSyncRequest{Ref: "acme", ToVersion: 2, Generation: plan.Generation})
	if code(err) != admin.CodeConflict {
		t.Errorf("SyncUpstream = %v, want conflict", err)
	}
}

func TestSyncIsRefusedWhenItHasNothingToApply(t *testing.T) {
	in, next := upgraded(t)
	if _, err := in.svc.AddUpstream(as("owner"), custom("hand-made")); err != nil {
		t.Fatal(err)
	}
	if _, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "hand-made"}); code(err) != admin.CodeInvalid {
		t.Errorf("planning a custom upstream = %v, want invalid", err)
	}
	if _, err := next.SyncUpstream(as("owner"), admin.UpstreamSyncRequest{Ref: "hand-made", ToVersion: 1}); code(err) != admin.CodeInvalid {
		t.Errorf("syncing a custom upstream = %v, want invalid", err)
	}
	if _, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "nope"}); code(err) != admin.CodeNotFound {
		t.Errorf("planning a missing upstream = %v, want not found", err)
	}
	plan, err := next.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	// The binary moved between the plan and the sync.
	if _, err := next.SyncUpstream(as("owner"), admin.UpstreamSyncRequest{Ref: "acme", ToVersion: 99, Generation: plan.Generation}); code(err) != admin.CodeConflict {
		t.Errorf("syncing to another version = %v, want conflict", err)
	}
	// The same version as the upstream has: the old binary, which has nothing newer.
	if _, err := in.svc.SyncUpstream(as("owner"), admin.UpstreamSyncRequest{Ref: "acme", ToVersion: 1, Generation: plan.Generation}); code(err) != admin.CodeConflict {
		t.Errorf("syncing to the same version = %v, want conflict", err)
	}
}

func TestAPresetThisKmanDoesNotHaveCannotBeSynced(t *testing.T) {
	in, _ := upgraded(t)
	gone := in.service(nil)
	if _, err := gone.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"}); code(err) != admin.CodeConflict {
		t.Errorf("PlanUpstreamSync = %v, want conflict", err)
	}
	// It still works, and shows no update.
	u, err := gone.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil || u.UpdateAvailable {
		t.Errorf("GetUpstream = %+v, %v", u, err)
	}
}

// A downgraded binary holds an older preset than the upstream records. That
// is not newer, so there is nothing to offer.
func TestAnOlderPresetIsNotOffered(t *testing.T) {
	v1, v2 := catalog()
	in := newInstallWith(t, v2)
	if _, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "acme", Preset: "acme"}); err != nil {
		t.Fatal(err)
	}
	plan, err := in.service(v1).PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "acme"})
	if err != nil || !plan.UpToDate || plan.FromVersion != 2 {
		t.Errorf("plan = %+v, %v", plan, err)
	}
}
