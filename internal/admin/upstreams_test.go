package admin_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/registry"
)

func ptr[T any](v T) *T { return &v }

// custom is a request for a custom upstream with no credential.
func custom(name string) admin.AddUpstreamRequest {
	return admin.AddUpstreamRequest{Name: name, UpstreamSettings: admin.UpstreamSettings{
		Host: ptr("api.example.com"), Paths: ptr([]string{"POST /v1/things"}),
	}}
}

func TestAddFromAPresetCopiesThePreset(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "my-groq", Preset: "groq"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "my-groq" || got.Preset != "groq" || got.PresetVersion != 1 {
		t.Errorf("name %q, preset %q at %d", got.Name, got.Preset, got.PresetVersion)
	}
	if !strings.HasPrefix(got.ID, "up_") || !got.CreatedAt.Equal(t0) {
		t.Errorf("id %q, created %v", got.ID, got.CreatedAt)
	}
	if got.Profile != "openai" || got.Host != "api.groq.com" || got.Port != 443 || !got.TLS || !got.InjectUsage {
		t.Errorf("profile %q, %s:%d, TLS %v, inject %v", got.Profile, got.Host, got.Port, got.TLS, got.InjectUsage)
	}
	if got.Auth != (admin.Auth{Style: "header", Header: "Authorization: Bearer {key}"}) {
		t.Errorf("auth = %+v", got.Auth)
	}
	if got.SDKEnv != "GROQ_API_KEY" || got.SDKBasePath != "/openai/v1" {
		t.Errorf("SDK env %q, base path %q", got.SDKEnv, got.SDKBasePath)
	}
	want := []string{"POST /openai/v1/chat/completions", "POST /openai/v1/responses", "GET /openai/v1/models", "GET /openai/v1/models/**"}
	if !slices.Equal(got.Paths, want) {
		t.Errorf("paths = %v, want %v", got.Paths, want)
	}
}

func TestAddFromAPresetNamesItAfterThePresetByDefault(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Preset: "twilio"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "twilio" || got.Auth != (admin.Auth{Style: "basic"}) {
		t.Errorf("got %+v", got)
	}
}

func TestAddFromAPresetTakesAnOverride(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Preset: "openai", UpstreamSettings: admin.UpstreamSettings{
		Host: ptr("eu.api.openai.com"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "eu.api.openai.com" || got.Preset != "openai" || got.SDKEnv != "OPENAI_API_KEY" {
		t.Errorf("got %+v", got)
	}
}

func TestAnUnknownPresetIsRefusedAndNamesTheKnownOnes(t *testing.T) {
	in := newInstall(t)
	_, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Preset: "nope"})
	if code(err) != admin.CodeInvalid || !strings.Contains(err.Error(), "anthropic") || !strings.Contains(err.Error(), "twilio") {
		t.Errorf("AddUpstream = %v, want invalid naming the presets", err)
	}
}

func TestACustomUpstreamIsBuiltFromSettings(t *testing.T) {
	in := newInstall(t)
	for _, tc := range []struct {
		name string
		req  admin.UpstreamSettings
		port int
		tls  bool
		auth admin.Auth
	}{
		{"defaults", admin.UpstreamSettings{}, 443, true, admin.Auth{Style: "header", Header: "Authorization: Bearer {key}"}},
		{"plain", admin.UpstreamSettings{TLS: ptr(false)}, 80, false, admin.Auth{Style: "header", Header: "Authorization: Bearer {key}"}},
		{"a port", admin.UpstreamSettings{Port: ptr(8443)}, 8443, true, admin.Auth{Style: "header", Header: "Authorization: Bearer {key}"}},
		{"a header", admin.UpstreamSettings{Auth: &admin.Auth{Style: "header", Header: "X-Key: {key}"}}, 443, true, admin.Auth{Style: "header", Header: "X-Key: {key}"}},
		{"basic", admin.UpstreamSettings{Auth: &admin.Auth{Style: "basic"}}, 443, true, admin.Auth{Style: "basic"}},
		{"basic with a user", admin.UpstreamSettings{Auth: &admin.Auth{Style: "basic", BasicUsername: "api"}}, 443, true, admin.Auth{Style: "basic", BasicUsername: "api"}},
		{"anthropic", admin.UpstreamSettings{Profile: ptr("anthropic")}, 443, true, admin.Auth{Style: "header", Header: "x-api-key: {key}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := custom(strings.ReplaceAll(tc.name, " ", "-"))
			s := tc.req
			s.Host, s.Paths = req.Host, req.Paths
			req.UpstreamSettings = s
			got, err := in.svc.AddUpstream(as("owner"), req)
			if err != nil {
				t.Fatal(err)
			}
			if got.Preset != "" || got.PresetVersion != 0 {
				t.Errorf("preset %q at %d", got.Preset, got.PresetVersion)
			}
			if got.Port != tc.port || got.TLS != tc.tls || got.Auth != tc.auth {
				t.Errorf("port %d, TLS %v, auth %+v", got.Port, got.TLS, got.Auth)
			}
			if tc.req.Profile == nil && got.Profile != "generic" {
				t.Errorf("profile %q, want generic", got.Profile)
			}
		})
	}
}

func TestABadUpstreamIsRefusedAndChangesNothing(t *testing.T) {
	in := newInstall(t)
	generation := in.reg.Generation()
	before := len(in.latest(t, 100))
	for name, req := range map[string]admin.AddUpstreamRequest{
		"no name":      {UpstreamSettings: custom("").UpstreamSettings},
		"a bad name":   custom("Not A Name"),
		"no host":      {Name: "x", UpstreamSettings: admin.UpstreamSettings{Paths: ptr([]string{"GET /"})}},
		"no paths":     {Name: "x", UpstreamSettings: admin.UpstreamSettings{Host: ptr("a.example.com")}},
		"a bad preset": {Name: "x", Preset: "NOPE"},
	} {
		if _, err := in.svc.AddUpstream(as("owner"), req); code(err) != admin.CodeInvalid {
			t.Errorf("%s: AddUpstream = %v, want invalid", name, err)
		}
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("the audit log grew by %d", after-before)
	}
	if g := in.reg.Generation(); g != generation {
		t.Errorf("the registry moved from generation %d to %d", generation, g)
	}
}

func TestUpstreamNamesAreUniqueAmongLiveUpstreams(t *testing.T) {
	in := newInstall(t)
	first, err := in.svc.AddUpstream(as("owner"), custom("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.AddUpstream(as("owner"), custom("a")); code(err) != admin.CodeConflict {
		t.Errorf("a second upstream named a = %v, want conflict", err)
	}
	if _, err := in.svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "a"}); err != nil {
		t.Fatal(err)
	}
	second, err := in.svc.AddUpstream(as("owner"), custom("a"))
	if err != nil {
		t.Fatalf("reusing a deleted upstream's name: %v", err)
	}
	if second.ID == first.ID {
		t.Error("the new upstream has the old one's id")
	}
}

func TestUpstreamsAreFoundByNameOrIdAndListedByName(t *testing.T) {
	in := newInstall(t)
	b, err := in.svc.AddUpstream(as("owner"), custom("b"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.AddUpstream(as("owner"), custom("a")); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"b", b.ID} {
		got, err := in.svc.GetUpstream(as("owner"), admin.UpstreamRef{Ref: ref})
		if err != nil || got.ID != b.ID {
			t.Errorf("GetUpstream(%s) = %+v, %v", ref, got, err)
		}
	}
	if _, err := in.svc.GetUpstream(as("owner"), admin.UpstreamRef{Ref: "nope"}); code(err) != admin.CodeNotFound {
		t.Errorf("GetUpstream(nope) = %v, want not found", err)
	}
	list, err := in.svc.ListUpstreams(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Upstreams) != 2 || list.Upstreams[0].Name != "a" || list.Upstreams[1].Name != "b" {
		t.Errorf("ListUpstreams = %+v", list)
	}
}

func TestReadsNeedACaller(t *testing.T) {
	in := newInstall(t)
	if _, err := in.svc.ListUpstreams(t.Context()); code(err) != admin.CodeUnauthorized {
		t.Errorf("ListUpstreams with no caller = %v", err)
	}
	if _, err := in.svc.GetUpstream(t.Context(), admin.UpstreamRef{Ref: "a"}); code(err) != admin.CodeUnauthorized {
		t.Errorf("GetUpstream with no caller = %v", err)
	}
}

func TestAddingAnUpstreamIsAuditedAndPublished(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	e := in.latest(t, 1)[0]
	if want := "owner upstream.create upstream/" + got.ID; summary(e) != want {
		t.Errorf("audit = %s, want %s", summary(e), want)
	}
	if !strings.Contains(string(e.After), `"host":"api.openai.com"`) || !strings.Contains(string(e.After), `"preset":"openai"`) {
		t.Errorf("the entry records %s", e.After)
	}
	u, ok := in.reg.Upstream(got.ID)
	if !ok {
		t.Fatal("the registry has no such upstream")
	}
	if u.Name != "openai" || u.Host != "api.openai.com" || u.Port != 443 || !u.TLS || u.PresetVersion != 1 || u.Preset != "openai" ||
		u.Auth != (registry.Auth{Style: "header", Header: "Authorization: Bearer {key}"}) || u.SDKEnv != "OPENAI_API_KEY" || u.PriceMultiplierPPM != 1_000_000 {
		t.Errorf("registry upstream = %+v", u)
	}
	if u.Generation == 0 || u.Generation != in.reg.Generation() {
		t.Errorf("registry upstream at generation %d, registry at %d", u.Generation, in.reg.Generation())
	}
}

func TestUpdatingAnUpstreamChangesOnlyWhatIsGiven(t *testing.T) {
	in := newInstall(t)
	before, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	later := t0.Add(time.Minute)
	in.now = later
	got, err := in.svc.UpdateUpstream(as("ci"), admin.UpstreamUpdate{Ref: "openai", Name: ptr("primary"),
		UpstreamSettings: admin.UpstreamSettings{Host: ptr("gw.example.com"), Paths: ptr([]string{"POST /v1/chat/completions"})}})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != before.ID || got.Name != "primary" || got.Host != "gw.example.com" || !slices.Equal(got.Paths, []string{"POST /v1/chat/completions"}) {
		t.Errorf("got %+v", got)
	}
	if got.SDKEnv != "OPENAI_API_KEY" || got.Preset != "openai" || !got.InjectUsage || !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(later) {
		t.Errorf("kept %+v", got)
	}
	e := in.latest(t, 1)[0]
	if want := "ci upstream.update upstream/" + got.ID; summary(e) != want {
		t.Errorf("audit = %s, want %s", summary(e), want)
	}
	if !strings.Contains(string(e.Before), `"host":"api.openai.com"`) || !strings.Contains(string(e.After), `"host":"gw.example.com"`) {
		t.Errorf("the entry records %s -> %s", e.Before, e.After)
	}
	if u, _ := in.reg.Upstream(got.ID); u.Host != "gw.example.com" || u.Name != "primary" {
		t.Errorf("the registry holds %+v", u)
	}
}

func TestAnUpstreamUpdateThatChangesNothingIsNotAudited(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	before, generation := len(in.latest(t, 100)), in.reg.Generation()
	same, err := in.svc.UpdateUpstream(as("owner"), admin.UpstreamUpdate{Ref: "openai", Name: ptr("openai"),
		UpstreamSettings: admin.UpstreamSettings{Host: ptr(got.Host)}})
	if err != nil || same.ID != got.ID {
		t.Fatalf("UpdateUpstream = %+v, %v", same, err)
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("the audit log grew by %d", after-before)
	}
	if g := in.reg.Generation(); g != generation {
		t.Errorf("the registry moved to generation %d", g)
	}
}

func TestABadUpdateIsRefused(t *testing.T) {
	in := newInstall(t)
	for _, n := range []string{"a", "b"} {
		if _, err := in.svc.AddUpstream(as("owner"), custom(n)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		req  admin.UpstreamUpdate
		want admin.Code
	}{
		{"missing", admin.UpstreamUpdate{Ref: "nope", Name: ptr("c")}, admin.CodeNotFound},
		{"a taken name", admin.UpstreamUpdate{Ref: "a", Name: ptr("b")}, admin.CodeConflict},
		{"a bad name", admin.UpstreamUpdate{Ref: "a", Name: ptr("Bad Name")}, admin.CodeInvalid},
		{"no paths", admin.UpstreamUpdate{Ref: "a", UpstreamSettings: admin.UpstreamSettings{Paths: &[]string{}}}, admin.CodeInvalid},
		{"a bad port", admin.UpstreamUpdate{Ref: "a", UpstreamSettings: admin.UpstreamSettings{Port: ptr(70000)}}, admin.CodeInvalid},
	} {
		if _, err := in.svc.UpdateUpstream(as("owner"), tc.req); code(err) != tc.want {
			t.Errorf("%s: UpdateUpstream = %v, want %s", tc.name, err, tc.want)
		}
	}
}

func TestRemovingAnUpstreamTakesItOutOfTheRegistry(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.AddUpstream(as("owner"), custom("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := in.reg.Upstream(got.ID); ok {
		t.Error("the registry still holds it")
	}
	if want := "owner upstream.delete upstream/" + got.ID; summary(in.latest(t, 1)[0]) != want {
		t.Errorf("audit = %s, want %s", summary(in.latest(t, 1)[0]), want)
	}
	if _, err := in.svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "a"}); code(err) != admin.CodeNotFound {
		t.Errorf("a second remove = %v, want not found", err)
	}
}
