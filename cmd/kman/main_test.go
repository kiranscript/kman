package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/pricing"
)

func TestAnUnknownCommandIsAUsageErrorNamingEveryCommand(t *testing.T) {
	s, _, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"nonsense"}, s); code != 1 {
		t.Errorf("kman nonsense exited %d, want 1 (SPEC §12: usage errors are errors)", code)
	}
	for _, name := range []string{"init", "serve", "keygen", "version", "login", "logout", "admin-token", "settings", "audit", "price"} {
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

func TestPlainKmanAsksForAnUpgradePastSixtyDays(t *testing.T) {
	now := pricing.Builtin().GeneratedAt().AddDate(0, 0, 61)
	s, stdout, _ := testSys(t, map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()})
	s.now = func() time.Time { return now }
	run(context.Background(), nil, s)
	if out := stdout.String(); !strings.Contains(out, "price data is 61 days old, upgrade kman") {
		t.Errorf("plain kman printed:\n%s", out)
	}
}
