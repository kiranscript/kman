package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/store"
)

func TestAnUnknownCommandIsAUsageErrorNamingEveryCommand(t *testing.T) {
	s, _, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"nonsense"}, s); code != 1 {
		t.Errorf("kman nonsense exited %d, want 1 (SPEC §12: usage errors are errors)", code)
	}
	for _, name := range []string{"init", "serve", "keygen", "rekey", "version", "login", "logout", "admin-token", "settings", "audit", "price"} {
		if !strings.Contains(stderr.String(), name) {
			t.Errorf("the usage line lacks %s: %s", name, stderr)
		}
	}
}

func TestOnlineCommandsReadTheCredentialsFileInTheDefaultConfigDir(t *testing.T) {
	home := t.TempDir()
	s, _, stderr := testSys(t, map[string]string{"HOME": home})
	if code := run(t.Context(), []string{"admin-token", "list"}, s); code != 1 {
		t.Fatalf("admin-token list with no credentials exited %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "kman login") {
		t.Errorf("stderr %q does not say to log in", stderr)
	}
}

func TestTheBuiltinPricesReachAdmin(t *testing.T) {
	p := builtinPrices()
	if p.GeneratedAt.IsZero() || p.StaleAfter != 60*24*time.Hour {
		t.Errorf("table header: %v, stale after %v", p.GeneratedAt, p.StaleAfter)
	}
	var gpt, groq *admin.PriceModel
	for i, m := range p.Models {
		switch {
		case m.Preset == "" && m.Model == "gpt-5.4":
			gpt = &p.Models[i]
		case m.Preset == "groq" && m.Model == "openai/gpt-oss-120b":
			groq = &p.Models[i]
		}
	}
	if gpt == nil || groq == nil {
		t.Fatalf("missing rows: gpt-5.4 %v, groq %v", gpt, groq)
	}
	if gpt.InputMicro != 2500000 || gpt.OutputMicro != 15000000 || *gpt.CachedMicro != 250000 || gpt.MaxOutput != 128000 || !gpt.Reviewed {
		t.Errorf("gpt-5.4 = %+v", *gpt)
	}
	if len(gpt.LongContext) != 1 || gpt.LongContext[0] != (admin.PriceBand{AboveTokens: 272000, InputMicro: 5000000, OutputMicro: 22500000}) {
		t.Errorf("gpt-5.4 long context = %+v", gpt.LongContext)
	}
	if !slices.Contains(gpt.ServiceTiers, "flex") || !slices.IsSorted(gpt.ServiceTiers) {
		t.Errorf("gpt-5.4 tiers = %v", gpt.ServiceTiers)
	}
}

func TestPlainKmanShowsThisBinarysPriceDataAndTheCommands(t *testing.T) {
	now := pricing.Builtin().GeneratedAt().AddDate(0, 0, 2)
	s, stdout, _ := testSys(t, map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()})
	s.now = func() time.Time { return now }
	if code := run(context.Background(), nil, s); code != 0 {
		t.Fatalf("plain kman exited %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "2 days old") || !strings.Contains(out, "usage: kman") {
		t.Errorf("plain kman printed:\n%s", out)
	}
}

// The date a release build stamps is the commit's, and plain kman counts its age
// from it (SPEC §12).
func TestPlainKmanShowsHowOldThisBinaryIs(t *testing.T) {
	old := date
	t.Cleanup(func() { date = old })
	date = "2026-10-30T08:00:00Z"
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s, stdout, _ := testSys(t, map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()})
	s.now = func() time.Time { return now }
	run(context.Background(), nil, s)
	if out := stdout.String(); !strings.Contains(out, "This kman binary was built 2026-10-30, 6 days old.\n") {
		t.Errorf("plain kman printed:\n%s", out)
	}
}

func TestPlainKmanSaysWhenThisBinaryHasNoBuildDate(t *testing.T) {
	old := date
	t.Cleanup(func() { date = old })
	date = "not a date"
	s, stdout, _ := testSys(t, map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()})
	run(context.Background(), nil, s)
	if out := stdout.String(); !strings.Contains(out, "age is unknown") {
		t.Errorf("plain kman printed:\n%s", out)
	}
}

func TestPlainKmanAsksForAnUpgradePastSixtyDays(t *testing.T) {
	now := pricing.Builtin().GeneratedAt().AddDate(0, 0, 61)
	s, stdout, _ := testSys(t, map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()})
	s.now = func() time.Time { return now }
	run(context.Background(), nil, s)
	if out := stdout.String(); !strings.Contains(out, "price data is 61 days old, upgrade kman") {
		t.Errorf("plain kman printed:\n%s", out)
	}
}

func TestAdminsOverridesReachThePricesInForce(t *testing.T) {
	ctx := admin.WithCaller(t.Context(), admin.Caller{Actor: "owner"})
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return at }
	st, err := store.Open(ctx, store.Options{Dir: t.TempDir(), Now: at})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	prices := pricing.NewPrices(pricing.Builtin())
	svc := admin.New(admin.Options{Store: st, Now: now, ApplyPrices: publishOverrides(prices)})
	micro := func(n int64) *int64 { return &n }
	cached := micro(500_000)
	req := admin.PriceOverrideSet{Model: "gpt-5.4", InputMicro: micro(1_000_000), OutputMicro: micro(2_000_000), MaxOutput: micro(1000)}
	req.CachedMicro = cached
	if _, err := svc.SetPriceOverride(ctx, req); err != nil {
		t.Fatal(err)
	}
	e, origin, ok := prices.Table().Resolve("", "", "gpt-5.4")
	if !ok || origin != pricing.OriginInstallOverride || e.Input != 1_000_000 || e.Output != 2_000_000 || e.MaxOutput != 1000 || e.Cached == nil || *e.Cached != 500_000 {
		t.Errorf("after set: %+v, %v, %v", e, origin, ok)
	}

	// A restart loads them from the store.
	restarted := pricing.NewPrices(pricing.Builtin())
	if err := admin.New(admin.Options{Store: st, Now: now, ApplyPrices: publishOverrides(restarted)}).Load(ctx); err != nil {
		t.Fatal(err)
	}
	if _, origin, _ := restarted.Table().Resolve("", "", "gpt-5.4"); origin != pricing.OriginInstallOverride {
		t.Errorf("after a restart, origin = %v", origin)
	}

	if _, err := svc.RemovePriceOverride(ctx, admin.PriceOverrideRef{Model: "gpt-5.4"}); err != nil {
		t.Fatal(err)
	}
	if _, origin, ok := prices.Table().Resolve("", "", "gpt-5.4"); !ok || origin != pricing.OriginGlobal {
		t.Errorf("after rm: origin %v, %v", origin, ok)
	}
}

func TestPublishOverridesKeepsUpstreamsApart(t *testing.T) {
	prices := pricing.NewPrices(pricing.Builtin())
	err := publishOverrides(prices)([]admin.PriceOverride{{UpstreamID: "up_a", Model: "m", InputMicro: 1, OutputMicro: 2, MaxOutput: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, origin, ok := prices.Table().Resolve("up_a", "", "m"); !ok || origin != pricing.OriginUpstreamOverride {
		t.Errorf("on its upstream: %v, %v", origin, ok)
	}
	if _, _, ok := prices.Table().Resolve("up_b", "", "m"); ok {
		t.Error("it reached another upstream")
	}
}

func TestABadOverrideIsRefusedAndNamed(t *testing.T) {
	prices := pricing.NewPrices(pricing.Builtin())
	err := publishOverrides(prices)([]admin.PriceOverride{{Model: "m", MaxOutput: 0}})
	if err == nil || !strings.Contains(err.Error(), "m") {
		t.Errorf("err = %v", err)
	}
}
