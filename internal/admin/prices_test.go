package admin_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

func priced(t *testing.T, generated time.Time) (*install, *admin.Service) {
	t.Helper()
	in := newInstall(t)
	return in, admin.New(admin.Options{
		Store: in.store,
		Now:   func() time.Time { return in.now },
		Prices: func() admin.PriceTable {
			return admin.PriceTable{GeneratedAt: generated, StaleAfter: 60 * 24 * time.Hour, Models: []admin.PriceModel{
				{Model: "gpt-5.4", Mode: "chat", InputMicro: 2500000, OutputMicro: 15000000, MaxOutput: 128000},
				{Preset: "groq", Model: "openai/gpt-oss-120b", Mode: "chat", InputMicro: 150000, OutputMicro: 600000, MaxOutput: 65536},
				{Preset: "together", Model: "openai/gpt-oss-120b", Mode: "chat", InputMicro: 150000, OutputMicro: 600000, MaxOutput: 131072},
			}}
		},
	})
}

func TestPriceDataStatusGivesTheAge(t *testing.T) {
	_, svc := priced(t, t0.AddDate(0, 0, -3).Add(-time.Hour))
	got, err := svc.PriceDataStatus(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.GeneratedAt.Equal(t0.AddDate(0, 0, -3).Add(-time.Hour)) || got.AgeDays != 3 || got.AgeSeconds != 3*86400+3600 {
		t.Errorf("status = %+v; want 3 days old", got)
	}
	if got.Stale || got.Notice != "" {
		t.Errorf("status = %+v; want no upgrade notice at 3 days", got)
	}
}

func TestPriceDataPastSixtyDaysAsksForAnUpgrade(t *testing.T) {
	for _, tt := range []struct {
		name  string
		age   time.Duration
		stale bool
		days  int64
	}{
		{"exactly 60 days", 60 * 24 * time.Hour, false, 60},
		{"just past 60 days", 60*24*time.Hour + time.Second, true, 60},
		{"73 days", 73 * 24 * time.Hour, true, 73},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, svc := priced(t, t0.Add(-tt.age))
			got, err := svc.PriceDataStatus(as("owner"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Stale != tt.stale || got.AgeDays != tt.days {
				t.Errorf("status = %+v", got)
			}
			if tt.stale && !strings.Contains(got.Notice, "days old, upgrade kman") {
				t.Errorf("notice = %q", got.Notice)
			}
		})
	}
}

func TestPriceDataWithAClockBehindTheTableIsNotOld(t *testing.T) {
	_, svc := priced(t, t0.AddDate(0, 0, 2))
	got, err := svc.PriceDataStatus(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if got.AgeSeconds != 0 || got.AgeDays != 0 || got.Stale {
		t.Errorf("status = %+v", got)
	}
}

func TestListPricesShowsTheTableAndItsAge(t *testing.T) {
	_, svc := priced(t, t0.AddDate(0, 0, -3))
	got, err := svc.ListPrices(as("owner"), admin.PriceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.AgeDays != 3 || len(got.Models) != 3 {
		t.Errorf("list = %+v", got)
	}
}

func TestListPricesFilters(t *testing.T) {
	_, svc := priced(t, t0)
	for _, tt := range []struct {
		name string
		q    admin.PriceQuery
		want int
	}{
		{"a preset", admin.PriceQuery{Preset: "groq"}, 1},
		{"the global section", admin.PriceQuery{Preset: "global"}, 1},
		{"part of a model id, in any case", admin.PriceQuery{Model: "OSS"}, 2},
		{"both", admin.PriceQuery{Preset: "together", Model: "oss"}, 1},
		{"nothing matches", admin.PriceQuery{Model: "nope"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.ListPrices(as("owner"), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Models) != tt.want {
				t.Errorf("got %d models: %+v", len(got.Models), got.Models)
			}
			if got.Models == nil {
				t.Error("models is null, want a list")
			}
		})
	}
}

func TestPricesNeedACaller(t *testing.T) {
	_, svc := priced(t, t0)
	if _, err := svc.ListPrices(context.Background(), admin.PriceQuery{}); code(err) != admin.CodeUnauthorized {
		t.Errorf("ListPrices with no caller: %v", err)
	}
	if _, err := svc.PriceDataStatus(context.Background()); code(err) != admin.CodeUnauthorized {
		t.Errorf("PriceDataStatus with no caller: %v", err)
	}
}

func TestReadingPricesChangesNothing(t *testing.T) {
	in, svc := priced(t, t0)
	if _, err := svc.ListPrices(as("owner"), admin.PriceQuery{}); err != nil {
		t.Fatal(err)
	}
	page, err := svc.Audit(as("owner"), admin.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || len(in.applied) != 1 { // the owner token, nothing else
		t.Errorf("audit = %+v, applied %+v", page.Entries, in.applied)
	}
}
