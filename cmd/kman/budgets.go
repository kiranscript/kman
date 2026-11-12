package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/policy"
)

// alertLimits reads a key's policy as the amounts its spend warnings count from.
// alerts may not import policy (SPEC §2), so it is adapted here.
func alertLimits(policyJSON string) (alerts.Limits, error) {
	p, err := policy.Parse(policyJSON)
	if err != nil {
		return alerts.Limits{}, err
	}
	return alerts.Limits{
		DailyCapMicro: p.DailyCapMicro, MonthlyCapMicro: p.MonthlyCapMicro,
		DailyBudgetMicro: p.DailyBudgetMicro, MonthlyBudgetMicro: p.MonthlyBudgetMicro,
		Percents: p.SoftBudgetPercents,
	}, nil
}

// alertSettings reads the settings that spend warnings use from admin, which
// alerts may not import. The soft_budget_defaults setting is read at each check,
// so a change applies at once. The spend_timezone is read once and kept: the
// meter reads it once too, and a change to it takes effect when kman restarts,
// so a day means the same to the warnings as to the meter.
func alertSettings(service func() *admin.Service) func(context.Context) (alerts.Settings, error) {
	var (
		mu  sync.Mutex
		loc *time.Location
	)
	return func(ctx context.Context) (alerts.Settings, error) {
		svc := service()
		if svc == nil {
			return alerts.Settings{}, fmt.Errorf("the admin service is not up yet")
		}
		st, err := svc.Settings(admin.WithCaller(ctx, admin.Caller{Actor: "system:serve"})) // a read, which is not audited
		if err != nil {
			return alerts.Settings{}, err
		}
		mu.Lock()
		defer mu.Unlock()
		if loc == nil {
			if loc, err = time.LoadLocation(st.SpendTimezone); err != nil {
				return alerts.Settings{}, fmt.Errorf("the spend_timezone setting, %q: %w", st.SpendTimezone, err)
			}
		}
		return alerts.Settings{SoftBudgets: st.SoftBudgetPercents, Location: loc}, nil
	}
}
