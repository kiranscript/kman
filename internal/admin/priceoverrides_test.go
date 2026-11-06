package admin_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/profiles"
)

// overrides is an install with a small built-in table, recording what the
// request path is told. The table has gpt-5.4 globally, and openai/gpt-oss-120b
// on groq and together.
type overrides struct {
	*install
	svc       *admin.Service
	published [][]admin.PriceOverride
}

func newOverrides(t *testing.T) *overrides {
	t.Helper()
	in := newInstall(t)
	o := &overrides{install: in}
	o.svc = o.service(in)
	return o
}

// service is a service over the install, as a restart of kman would have.
func (o *overrides) service(in *install) *admin.Service {
	return admin.New(admin.Options{
		Store: in.store,
		Now:   func() time.Time { return in.now },
		Vault: in.vault,
		Prices: func() admin.PriceTable {
			return admin.PriceTable{GeneratedAt: t0, StaleAfter: 60 * 24 * time.Hour, Models: []admin.PriceModel{
				{Model: "gpt-5.4", Mode: "chat", InputMicro: 2500000, OutputMicro: 15000000, MaxOutput: 128000},
				{Preset: "groq", Model: "openai/gpt-oss-120b", Mode: "chat", InputMicro: 150000, OutputMicro: 600000, MaxOutput: 65536},
				{Preset: "together", Model: "openai/gpt-oss-120b", Mode: "chat", InputMicro: 150000, OutputMicro: 600000, MaxOutput: 131072},
			}}
		},
		Presets: profiles.Presets(),
		ApplyPrices: func(rows []admin.PriceOverride) error {
			o.published = append(o.published, rows)
			return nil
		},
	})
}

func price(n int64) *int64 { return &n }

// setReq is an override of model with every required price.
func setReq(upstream, model string) admin.PriceOverrideSet {
	return admin.PriceOverrideSet{Upstream: upstream, Model: model, InputMicro: price(3000000), OutputMicro: price(9000000), MaxOutput: price(2000)}
}

func (o *overrides) addCustom(t *testing.T, name string) admin.Upstream {
	t.Helper()
	u, err := o.svc.AddUpstream(as("owner"), custom(name))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSetPriceOverrideInstallWide(t *testing.T) {
	o := newOverrides(t)
	got, err := o.svc.SetPriceOverride(as("ci"), setReq("", "new-model"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Upstream != "" || got.UpstreamID != "" || got.Model != "new-model" || got.InputMicro != 3000000 || got.OutputMicro != 9000000 || got.MaxOutput != 2000 {
		t.Errorf("override = %+v", got)
	}
	if got.Actor != "ci" || !got.UpdatedAt.Equal(t0) {
		t.Errorf("actor %q at %v", got.Actor, got.UpdatedAt)
	}
	if len(got.Hides) != 0 {
		t.Errorf("hides %v, but the table lacks the model", got.Hides)
	}
}

func TestSetPriceOverrideOnAnUpstream(t *testing.T) {
	o := newOverrides(t)
	u := o.addCustom(t, "acme")
	for _, ref := range []string{"acme", u.ID} {
		got, err := o.svc.SetPriceOverride(as("owner"), setReq(ref, "m-"+ref))
		if err != nil {
			t.Fatal(err)
		}
		if got.Upstream != "acme" || got.UpstreamID != u.ID {
			t.Errorf("by %s: override = %+v", ref, got)
		}
	}
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("nope", "m")); code(err) != admin.CodeNotFound {
		t.Errorf("on no such upstream: %v", err)
	}
}

func TestSetPriceOverrideKeepsOptionalPricesApartFromZero(t *testing.T) {
	o := newOverrides(t)
	req := setReq("", "m")
	req.CachedMicro, req.AudioInputMicro = price(0), price(7)
	if _, err := o.svc.SetPriceOverride(as("owner"), req); err != nil {
		t.Fatal(err)
	}
	list, err := o.svc.ListPrices(as("owner"), admin.PriceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got := list.Overrides[0]
	if got.CachedMicro == nil || *got.CachedMicro != 0 || got.AudioInputMicro == nil || *got.AudioInputMicro != 7 {
		t.Errorf("optional prices = %+v", got)
	}
	if got.CacheWriteMicro != nil || got.CacheWrite1hMicro != nil || got.AudioOutputMicro != nil || got.AudioCachedMicro != nil {
		t.Errorf("prices that were never set = %+v", got)
	}
}

func TestSetPriceOverrideRefuses(t *testing.T) {
	o := newOverrides(t)
	for _, tt := range []struct {
		name string
		mod  func(*admin.PriceOverrideSet)
		want string
	}{
		{"no input", func(r *admin.PriceOverrideSet) { r.InputMicro = nil }, "input_micro"},
		{"no output", func(r *admin.PriceOverrideSet) { r.OutputMicro = nil }, "output_micro"},
		{"no max output", func(r *admin.PriceOverrideSet) { r.MaxOutput = nil }, "max_output"},
		{"zero max output", func(r *admin.PriceOverrideSet) { r.MaxOutput = price(0) }, "max_output"},
		{"a negative input", func(r *admin.PriceOverrideSet) { r.InputMicro = price(-1) }, "input_micro"},
		{"a negative output", func(r *admin.PriceOverrideSet) { r.OutputMicro = price(-1) }, "output_micro"},
		{"a negative optional price", func(r *admin.PriceOverrideSet) { r.CacheWrite1hMicro = price(-1) }, "cache_write_1h_micro"},
		{"no model", func(r *admin.PriceOverrideSet) { r.Model = "" }, "model"},
		{"a model with a space", func(r *admin.PriceOverrideSet) { r.Model = "gpt 5" }, "model"},
		{"a model with a control character", func(r *admin.PriceOverrideSet) { r.Model = "gpt\n5" }, "model"},
		{"a model too long to be one", func(r *admin.PriceOverrideSet) { r.Model = strings.Repeat("m", 257) }, "model"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := setReq("", "m")
			tt.mod(&req)
			_, err := o.svc.SetPriceOverride(as("owner"), req)
			if code(err) != admin.CodeInvalid || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want invalid naming %q", err, tt.want)
			}
		})
	}
	if got := o.published; len(got) != 0 {
		t.Errorf("a refused override was published: %+v", got)
	}
}

func TestZeroPricesAreAllowed(t *testing.T) {
	o := newOverrides(t)
	req := setReq("", "local-model")
	req.InputMicro, req.OutputMicro = price(0), price(0)
	if _, err := o.svc.SetPriceOverride(as("owner"), req); err != nil {
		t.Errorf("a free model: %v", err)
	}
}

func TestSetPriceOverrideIsAudited(t *testing.T) {
	o := newOverrides(t)
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("", "m")); err != nil {
		t.Fatal(err)
	}
	next := setReq("", "m")
	next.InputMicro = price(4000000)
	if _, err := o.svc.SetPriceOverride(as("ci"), next); err != nil {
		t.Fatal(err)
	}
	entries := o.latest(t, 2)
	if got, want := summary(entries[0]), "owner price_override.set price_override/install/m"; got != want {
		t.Errorf("first entry = %q, want %q", got, want)
	}
	if got, want := summary(entries[1]), "ci price_override.set price_override/install/m"; got != want {
		t.Errorf("second entry = %q, want %q", got, want)
	}
	if entries[0].Before != nil {
		t.Errorf("first entry has a before: %s", entries[0].Before)
	}
	var before, after admin.PriceOverride
	if err := json.Unmarshal(entries[1].Before, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entries[1].After, &after); err != nil {
		t.Fatal(err)
	}
	if before.InputMicro != 3000000 || after.InputMicro != 4000000 {
		t.Errorf("before %+v, after %+v", before, after)
	}
}

func TestSettingTheSamePricesChangesNothing(t *testing.T) {
	o := newOverrides(t)
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("", "m")); err != nil {
		t.Fatal(err)
	}
	before := len(o.published)
	again, err := o.svc.SetPriceOverride(as("ci"), setReq("", "m"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Actor != "owner" {
		t.Errorf("actor = %q, want who really set it", again.Actor)
	}
	if entries := o.latest(t, 1); entries[0].Actor != "owner" {
		t.Errorf("newest audit entry = %+v", entries[0])
	}
	if len(o.published) != before {
		t.Errorf("published again: %+v", o.published)
	}
}

func TestRemovePriceOverride(t *testing.T) {
	o := newOverrides(t)
	u := o.addCustom(t, "acme")
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("", "m")); err != nil {
		t.Fatal(err)
	}
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("acme", "m")); err != nil {
		t.Fatal(err)
	}
	got, err := o.svc.RemovePriceOverride(as("ci"), admin.PriceOverrideRef{Upstream: "acme", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if got.UpstreamID != u.ID || got.Model != "m" {
		t.Errorf("removed = %+v", got)
	}
	entry := o.latest(t, 1)[0]
	if want := "ci price_override.delete price_override/" + u.ID + "/m"; summary(entry) != want {
		t.Errorf("audit = %q, want %q", summary(entry), want)
	}
	if entry.Before == nil || entry.After != nil {
		t.Errorf("audit entry = %+v", entry)
	}
	list, err := o.svc.ListPrices(as("owner"), admin.PriceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Overrides) != 1 || list.Overrides[0].Upstream != "" {
		t.Errorf("after removal = %+v", list.Overrides)
	}
	if _, err := o.svc.RemovePriceOverride(as("owner"), admin.PriceOverrideRef{Upstream: "acme", Model: "m"}); code(err) != admin.CodeNotFound {
		t.Errorf("removing it twice: %v", err)
	}
	if _, err := o.svc.RemovePriceOverride(as("owner"), admin.PriceOverrideRef{}); code(err) != admin.CodeInvalid {
		t.Errorf("removing no model: %v", err)
	}
}

func TestEveryOverrideChangeIsPublishedWhole(t *testing.T) {
	o := newOverrides(t)
	u := o.addCustom(t, "acme")
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("", "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("acme", "b")); err != nil {
		t.Fatal(err)
	}
	last := o.published[len(o.published)-1]
	if len(last) != 2 || last[0].Model != "a" || last[0].UpstreamID != "" || last[1].Model != "b" || last[1].UpstreamID != u.ID {
		t.Errorf("published = %+v", last)
	}
	if _, err := o.svc.RemovePriceOverride(as("owner"), admin.PriceOverrideRef{Model: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.svc.RemovePriceOverride(as("owner"), admin.PriceOverrideRef{Upstream: "acme", Model: "b"}); err != nil {
		t.Fatal(err)
	}
	if last := o.published[len(o.published)-1]; last == nil || len(last) != 0 {
		t.Errorf("after removing them all, published %#v, want an empty set", last)
	}
}

func TestLoadPublishesTheStoredOverrides(t *testing.T) {
	o := newOverrides(t)
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("", "m")); err != nil {
		t.Fatal(err)
	}
	o.published = nil
	restarted := o.service(o.install)
	if err := restarted.Load(as("owner")); err != nil {
		t.Fatal(err)
	}
	if len(o.published) != 1 || len(o.published[0]) != 1 || o.published[0][0].Model != "m" {
		t.Errorf("published = %+v", o.published)
	}
}

func TestRemovingAnUpstreamRemovesItsOverrides(t *testing.T) {
	o := newOverrides(t)
	u := o.addCustom(t, "acme")
	for _, req := range []admin.PriceOverrideSet{setReq("acme", "m"), setReq("", "m")} {
		if _, err := o.svc.SetPriceOverride(as("owner"), req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.svc.RemoveUpstream(as("ci"), admin.UpstreamRef{Ref: "acme"}); err != nil {
		t.Fatal(err)
	}
	var said []string
	for _, e := range o.latest(t, 2) {
		said = append(said, summary(e))
	}
	if want := "ci price_override.delete price_override/" + u.ID + "/m"; !slices.Contains(said, want) {
		t.Errorf("audit = %v, want %q", said, want)
	}
	last := o.published[len(o.published)-1]
	if len(last) != 1 || last[0].UpstreamID != "" {
		t.Errorf("published = %+v", last)
	}
	list, err := o.svc.ListPrices(as("owner"), admin.PriceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Overrides) != 1 || list.Overrides[0].UpstreamID != "" {
		t.Errorf("list = %+v", list.Overrides)
	}
}

func TestAnUpstreamWithNoOverridesPublishesNoPrices(t *testing.T) {
	o := newOverrides(t)
	o.addCustom(t, "acme")
	if _, err := o.svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "acme"}); err != nil {
		t.Fatal(err)
	}
	if len(o.published) != 0 {
		t.Errorf("published = %+v", o.published)
	}
}

func TestListPricesMarksOverridesThatHideABuiltinEntry(t *testing.T) {
	o := newOverrides(t)
	if _, err := o.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "g", Preset: "groq"}); err != nil {
		t.Fatal(err)
	}
	o.addCustom(t, "acme")
	for _, req := range []admin.PriceOverrideSet{
		setReq("", "gpt-5.4"),                 // the global entry
		setReq("", "openai/gpt-oss-120b"),     // both presets' entries
		setReq("", "fresh"),                   // nothing
		setReq("g", "openai/gpt-oss-120b"),    // its preset's entry
		setReq("g", "gpt-5.4"),                // the global one, its preset having none
		setReq("acme", "gpt-5.4"),             // the global one
		setReq("acme", "openai/gpt-oss-120b"), // nothing: a custom upstream can't see a preset's entries
	} {
		if _, err := o.svc.SetPriceOverride(as("owner"), req); err != nil {
			t.Fatal(err)
		}
	}
	list, err := o.svc.ListPrices(as("owner"), admin.PriceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, ov := range list.Overrides {
		got[ov.Upstream+"|"+ov.Model] = ov.Hides
	}
	for key, want := range map[string][]string{
		"|gpt-5.4":                 {"global"},
		"|openai/gpt-oss-120b":     {"groq", "together"},
		"|fresh":                   nil,
		"g|openai/gpt-oss-120b":    {"groq"},
		"g|gpt-5.4":                {"global"},
		"acme|gpt-5.4":             {"global"},
		"acme|openai/gpt-oss-120b": nil,
	} {
		h, ok := got[key]
		if !ok {
			t.Errorf("%s is not listed in %v", key, got)
		}
		if !slices.Equal(h, want) {
			t.Errorf("%s hides %v, want %v", key, h, want)
		}
	}
}

func TestSetPriceOverrideSaysWhatItHides(t *testing.T) {
	o := newOverrides(t)
	got, err := o.svc.SetPriceOverride(as("owner"), setReq("", "gpt-5.4"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Hides, []string{"global"}) {
		t.Errorf("hides = %v", got.Hides)
	}
}

func TestListPricesFiltersOverrides(t *testing.T) {
	o := newOverrides(t)
	if _, err := o.svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "g", Preset: "groq"}); err != nil {
		t.Fatal(err)
	}
	o.addCustom(t, "acme")
	for _, req := range []admin.PriceOverrideSet{setReq("", "alpha"), setReq("g", "beta"), setReq("acme", "gamma")} {
		if _, err := o.svc.SetPriceOverride(as("owner"), req); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name string
		q    admin.PriceQuery
		want []string
	}{
		{"nothing narrows it", admin.PriceQuery{}, []string{"alpha", "beta", "gamma"}},
		{"part of a model, in any case", admin.PriceQuery{Model: "ETA"}, []string{"beta"}},
		{"a preset: install-wide ones and its upstreams'", admin.PriceQuery{Preset: "groq"}, []string{"alpha", "beta"}},
		{"global: install-wide ones and custom upstreams'", admin.PriceQuery{Preset: "global"}, []string{"alpha", "gamma"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			list, err := o.svc.ListPrices(as("owner"), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, ov := range list.Overrides {
				got = append(got, ov.Model)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOverridesListIsNeverNull(t *testing.T) {
	o := newOverrides(t)
	list, err := o.svc.ListPrices(as("owner"), admin.PriceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Overrides == nil {
		t.Error("overrides is null, want a list")
	}
}

func TestImportPriceOverrides(t *testing.T) {
	o := newOverrides(t)
	o.addCustom(t, "acme")
	if _, err := o.svc.SetPriceOverride(as("owner"), setReq("", "same")); err != nil {
		t.Fatal(err)
	}
	changed := setReq("", "changed")
	if _, err := o.svc.SetPriceOverride(as("owner"), changed); err != nil {
		t.Fatal(err)
	}
	changed.InputMicro = price(1)
	entriesBefore := len(o.published)
	got, err := o.svc.ImportPriceOverrides(as("ci"), admin.PriceOverrideImport{Overrides: []admin.PriceOverrideSet{setReq("", "same"), changed, setReq("acme", "new")}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed != 2 || got.Unchanged != 1 {
		t.Errorf("result = %+v", got)
	}
	if len(o.published) != entriesBefore+1 {
		t.Errorf("published %d times for one import", len(o.published)-entriesBefore)
	}
	var actions []string
	for _, e := range o.latest(t, 2) {
		actions = append(actions, e.Actor+" "+e.Action)
	}
	if !slices.Equal(actions, []string{"ci price_override.set", "ci price_override.set"}) {
		t.Errorf("audit = %v", actions)
	}
}

func TestImportIsAllOrNothing(t *testing.T) {
	o := newOverrides(t)
	bad := setReq("", "bad")
	bad.MaxOutput = nil
	for _, tt := range []struct {
		name string
		rows []admin.PriceOverrideSet
		code admin.Code
		want string
	}{
		{"a bad row names itself", []admin.PriceOverrideSet{setReq("", "good"), bad}, admin.CodeInvalid, "bad"},
		{"a missing upstream", []admin.PriceOverrideSet{setReq("", "good"), setReq("nope", "m")}, admin.CodeNotFound, "nope"},
		{"the same row twice", []admin.PriceOverrideSet{setReq("", "good"), setReq("", "good")}, admin.CodeInvalid, "good"},
		{"nothing at all", nil, admin.CodeInvalid, "no overrides"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := o.svc.ImportPriceOverrides(as("owner"), admin.PriceOverrideImport{Overrides: tt.rows})
			if code(err) != tt.code || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %s naming %q", err, tt.code, tt.want)
			}
			list, err := o.svc.ListPrices(as("owner"), admin.PriceQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Overrides) != 0 {
				t.Errorf("a refused import left %+v", list.Overrides)
			}
		})
	}
}

func TestOverrideCommandsNeedACaller(t *testing.T) {
	o := newOverrides(t)
	ctx := t.Context()
	if _, err := o.svc.SetPriceOverride(ctx, setReq("", "m")); code(err) != admin.CodeUnauthorized {
		t.Errorf("set: %v", err)
	}
	if _, err := o.svc.RemovePriceOverride(ctx, admin.PriceOverrideRef{Model: "m"}); code(err) != admin.CodeUnauthorized {
		t.Errorf("remove: %v", err)
	}
	if _, err := o.svc.ImportPriceOverrides(ctx, admin.PriceOverrideImport{Overrides: []admin.PriceOverrideSet{setReq("", "m")}}); code(err) != admin.CodeUnauthorized {
		t.Errorf("import: %v", err)
	}
}

func TestPriceMultiplier(t *testing.T) {
	in := newInstall(t)
	if u, err := in.svc.AddUpstream(as("owner"), custom("plain")); err != nil || u.PriceMultiplierPPM != 1_000_000 {
		t.Errorf("default multiplier = %d, %v", u.PriceMultiplierPPM, err)
	}
	uplift := int64(1_100_000)
	req := custom("eu")
	req.PriceMultiplierPPM = &uplift
	u, err := in.svc.AddUpstream(as("owner"), req)
	if err != nil {
		t.Fatal(err)
	}
	if u.PriceMultiplierPPM != uplift {
		t.Errorf("after add = %d", u.PriceMultiplierPPM)
	}
	pub, ok := in.reg.Upstream(u.ID)
	if !ok || pub.PriceMultiplierPPM != uplift {
		t.Errorf("published after add = %+v", pub)
	}

	next := int64(1_250_000)
	updated, err := in.svc.UpdateUpstream(as("ci"), admin.UpstreamUpdate{Ref: "eu", UpstreamSettings: admin.UpstreamSettings{PriceMultiplierPPM: &next}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PriceMultiplierPPM != next {
		t.Errorf("after update = %d", updated.PriceMultiplierPPM)
	}
	if pub, _ := in.reg.Upstream(u.ID); pub.PriceMultiplierPPM != next {
		t.Errorf("published after update = %d", pub.PriceMultiplierPPM)
	}
	entry := in.latest(t, 1)[0]
	if summary(entry) != "ci upstream.update upstream/"+u.ID || !strings.Contains(string(entry.Before), `"price_multiplier_ppm":1100000`) || !strings.Contains(string(entry.After), `"price_multiplier_ppm":1250000`) {
		t.Errorf("audit = %s: %s -> %s", summary(entry), entry.Before, entry.After)
	}
}

func TestPriceMultiplierRefuses(t *testing.T) {
	in := newInstall(t)
	if _, err := in.svc.AddUpstream(as("owner"), custom("a")); err != nil {
		t.Fatal(err)
	}
	for _, ppm := range []int64{0, -1, 100_000_001} {
		bad := ppm
		if _, err := in.svc.UpdateUpstream(as("owner"), admin.UpstreamUpdate{Ref: "a", UpstreamSettings: admin.UpstreamSettings{PriceMultiplierPPM: &bad}}); code(err) != admin.CodeInvalid {
			t.Errorf("update with %d: %v", ppm, err)
		}
		req := custom("b")
		req.PriceMultiplierPPM = &bad
		if _, err := in.svc.AddUpstream(as("owner"), req); code(err) != admin.CodeInvalid {
			t.Errorf("add with %d: %v", ppm, err)
		}
	}
}

func TestSyncKeepsThePriceMultiplier(t *testing.T) {
	v1, v2 := catalog()
	in := newInstallWith(t, v1)
	uplift := int64(1_300_000)
	req := admin.AddUpstreamRequest{Name: "a", Preset: "acme"}
	req.PriceMultiplierPPM = &uplift
	if _, err := in.svc.AddUpstream(as("owner"), req); err != nil {
		t.Fatal(err)
	}
	svc := in.service(v2)
	plan, err := svc.PlanUpstreamSync(as("owner"), admin.UpstreamRef{Ref: "a"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.SyncUpstream(as("owner"), admin.UpstreamSyncRequest{Ref: "a", ToVersion: plan.ToVersion, Generation: plan.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if got.PriceMultiplierPPM != uplift {
		t.Errorf("after sync = %d", got.PriceMultiplierPPM)
	}
}
