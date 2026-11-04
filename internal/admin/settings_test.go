package admin_test

import (
	"slices"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

const day = 24 * 60 * 60

func TestANewInstallHasTheDefaultSettings(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.Settings(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	want := admin.Settings{
		MaxKeyLifetimeSeconds: 365 * day,
		DefaultKeyTTLSeconds:  90 * day,
		SpendTimezone:         "UTC",
		SoftBudgetPercents:    []int{50, 80, 100},
	}
	if !equalSettings(got, want) {
		t.Errorf("Settings = %+v, want %+v", got, want)
	}
}

func TestUpdateSettingsChangesOnlyWhatItNames(t *testing.T) {
	in := newInstall(t)
	ttl, tz := int64(30*day), "America/New_York"
	got, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{DefaultKeyTTLSeconds: &ttl, SpendTimezone: &tz})
	if err != nil {
		t.Fatal(err)
	}
	want := admin.Settings{MaxKeyLifetimeSeconds: 365 * day, DefaultKeyTTLSeconds: 30 * day, SpendTimezone: tz, SoftBudgetPercents: []int{50, 80, 100}}
	if !equalSettings(got, want) {
		t.Errorf("UpdateSettings = %+v, want %+v", got, want)
	}
	again, err := in.svc.Settings(as("owner"))
	if err != nil || !equalSettings(again, want) {
		t.Errorf("Settings after the update = %+v, %v; want %+v", again, err, want)
	}
	pcts := []int{75, 100}
	if _, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{SoftBudgetPercents: &pcts}); err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range in.latest(t, 3) {
		actions = append(actions, e.TargetID)
	}
	if want := []string{"default_key_ttl", "spend_timezone", "soft_budget_defaults"}; !slices.Equal(actions, want) {
		t.Errorf("audited settings %v, want %v", actions, want)
	}
}

func TestAnUpdateThatChangesNothingIsNotAudited(t *testing.T) {
	in := newInstall(t)
	before := len(in.latest(t, 100))
	tz := "UTC"
	if _, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{SpendTimezone: &tz}); err != nil {
		t.Fatal(err)
	}
	if after := len(in.latest(t, 100)); after != before {
		t.Errorf("setting a value to itself wrote %d audit entries", after-before)
	}
}

func TestUpdateSettingsRefusesBadValues(t *testing.T) {
	in := newInstall(t)
	i := func(n int64) *int64 { return &n }
	s := func(v string) *string { return &v }
	p := func(v ...int) *[]int { return &v }
	for name, u := range map[string]admin.SettingsUpdate{
		"nothing":                      {},
		"a TTL past the lifetime":      {DefaultKeyTTLSeconds: i(400 * day)},
		"a lifetime under the TTL":     {MaxKeyLifetimeSeconds: i(30 * day)},
		"a zero lifetime (never)":      {MaxKeyLifetimeSeconds: i(0)},
		"a lifetime over ten years":    {MaxKeyLifetimeSeconds: i(3651 * day)},
		"a TTL under an hour":          {DefaultKeyTTLSeconds: i(59 * 60)},
		"an unknown time zone":         {SpendTimezone: s("Mars/Olympus")},
		"the local time zone":          {SpendTimezone: s("Local")},
		"an empty time zone":           {SpendTimezone: s("")},
		"percentages out of order":     {SoftBudgetPercents: p(80, 50)},
		"a repeated percentage":        {SoftBudgetPercents: p(50, 50)},
		"a zero percentage":            {SoftBudgetPercents: p(0, 50)},
		"a percentage over 1000":       {SoftBudgetPercents: p(50, 1001)},
		"more than ten percentages":    {SoftBudgetPercents: p(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)},
		"a fine TTL beside a bad zone": {DefaultKeyTTLSeconds: i(7 * day), SpendTimezone: s("Nowhere")},
	} {
		if _, err := in.svc.UpdateSettings(as("owner"), u); code(err) != admin.CodeInvalid {
			t.Errorf("%s = %v, want invalid", name, err)
		}
	}
	// A lifetime and a TTL may move together.
	if _, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{MaxKeyLifetimeSeconds: i(30 * day), DefaultKeyTTLSeconds: i(7 * day)}); err != nil {
		t.Errorf("lowering both together = %v", err)
	}
	// No soft budgets by default is allowed.
	if _, err := in.svc.UpdateSettings(as("owner"), admin.SettingsUpdate{SoftBudgetPercents: p()}); err != nil {
		t.Errorf("clearing the soft-budget defaults = %v", err)
	}
	got, err := in.svc.Settings(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendTimezone != "UTC" || got.DefaultKeyTTLSeconds != 7*day || len(got.SoftBudgetPercents) != 0 {
		t.Errorf("Settings = %+v", got)
	}
}

func equalSettings(a, b admin.Settings) bool {
	return a.MaxKeyLifetimeSeconds == b.MaxKeyLifetimeSeconds && a.DefaultKeyTTLSeconds == b.DefaultKeyTTLSeconds &&
		a.SpendTimezone == b.SpendTimezone && slices.Equal(a.SoftBudgetPercents, b.SoftBudgetPercents)
}
