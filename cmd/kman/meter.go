package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kiranscript/kman/internal/admin"
	"github.com/kiranscript/kman/internal/meter"
	"github.com/kiranscript/kman/internal/policy"
	"github.com/kiranscript/kman/internal/pricing"
	"github.com/kiranscript/kman/internal/store"
)

// startMeter makes the meter serve runs: over the SQLite counters, with the
// spend_timezone setting as it stands, and with this month's spend read back.
// A change to the setting takes effect at the next start.
func startMeter(ctx context.Context, s *sys, log *slog.Logger, svc *admin.Service, st *store.Store, prices *pricing.Prices) (*meter.Meter, error) {
	settings, err := svc.Settings(admin.WithCaller(ctx, admin.Caller{Actor: "system:serve"})) // a read, which is not audited
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(settings.SpendTimezone)
	if err != nil {
		return nil, fmt.Errorf("the spend_timezone setting, %q: %w", settings.SpendTimezone, err)
	}
	m := meter.New(meter.Options{Prices: prices, Counters: meter.SQLite(st), Location: loc, Now: s.now, Log: log})
	if err := m.Load(ctx, s.now()); err != nil {
		return nil, err
	}
	return m, nil
}

// keySpend is what the meter holds for a key, as admin shows it beside the
// key's caps. admin may not import meter or policy (SPEC §2), so it is adapted
// here. A nil meter, as before serve has made one, shows nothing spent.
func keySpend(m *meter.Meter, now time.Time, keyID, policyJSON string) admin.KeySpend {
	if m == nil {
		return admin.KeySpend{}
	}
	caps, _ := policy.Parse(policyJSON) // a policy kman can't read has no caps to show
	spent := m.Spend(keyID, now)
	return admin.KeySpend{
		Today: spendPeriod(spent.Day, caps.DailyCapMicro),
		Month: spendPeriod(spent.Month, caps.MonthlyCapMicro),
	}
}

func spendPeriod(p meter.Period, cap int64) admin.SpendPeriod {
	return admin.SpendPeriod{Period: p.Label, CostMicro: p.CostMicro, EstimatedMicro: p.EstimatedMicro, ReservedMicro: p.ReservedMicro,
		Requests: p.Requests, UnmeteredRequests: p.Unmetered, CapMicro: cap}
}
