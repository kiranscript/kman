package alerts_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/store"
)

// limitsOf is what the composition root hands the alerts service: a key's
// policy read as the limits that warnings count from.
func limitsOf(raw string) (alerts.Limits, error) {
	p, err := policy.Parse(raw)
	if err != nil {
		return alerts.Limits{}, err
	}
	return alerts.Limits{
		DailyCapMicro: p.DailyCapMicro, MonthlyCapMicro: p.MonthlyCapMicro,
		DailyBudgetMicro: p.DailyBudgetMicro, MonthlyBudgetMicro: p.MonthlyBudgetMicro,
		Percents: p.SoftBudgetPercents,
	}, nil
}

// newBudgetInstall is an install whose alerts service knows how to read a policy.
func newBudgetInstall(t *testing.T, mutate ...func(*alerts.Options)) *install {
	t.Helper()
	return newInstall(t, append([]func(*alerts.Options){func(o *alerts.Options) { o.Limits = limitsOf }}, mutate...)...)
}

// keyWith makes an active standard key with a policy, as admin would.
func (in *install) keyWith(id, name, policyJSON string, mutate ...func(*store.VirtualKey)) {
	in.t.Helper()
	k := store.VirtualKey{ID: id, Name: name, CreatedAt: t0.AddDate(0, -1, 0), PolicyJSON: policyJSON, TagsJSON: "[]", PolicyVersion: 1,
		ExpiresAt: t0.AddDate(0, 6, 0)}
	for _, m := range mutate {
		m(&k)
	}
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.CreateKey(k, []byte("hash-"+id))
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
}

// spent records what a key spent on a day, as the meter's flush does.
func (in *install) spent(id, day string, micro int64) {
	in.t.Helper()
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		return tx.AddSpend([]store.SpendRow{{KeyID: id, Day: day, CostMicro: micro, Requests: 1}})
	})
	if err != nil {
		in.t.Fatal(err)
	}
}

// check is the warnings producers' turn at now, then whatever it queued is delivered.
func (in *install) check(now time.Time) {
	in.t.Helper()
	if err := in.svc.Check(context.Background(), now); err != nil {
		in.t.Fatal(err)
	}
	synctest.Wait()
}

// bodies are what a host was sent so far.
func (in *install) bodies(host string) []string {
	var out []string
	for _, r := range in.net.received(host) {
		out = append(out, r.Body)
	}
	return out
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("the alert lacks %q:\n%s", w, body)
		}
	}
}

// A day's spend crossing the default 80% of its cap alerts, naming the key, how
// much it has spent of what, and what to type next. Never again for that day.
func TestACapCrossedAtTheDefaultPercentAlertsOncePerPeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-04", 4_000_000)
		in.check(t0)
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("an alert at 40%% of the cap: %v", got)
		}

		in.spent("abcd1234", "2026-11-04", 4_500_000) // 85%
		in.check(t0)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts at 85%%, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "MEDIUM", "80%", "daily cap", "my-app", "abcd1234", "$8.50", "$10", "2026-11-04", "kman key show abcd1234")

		in.spent("abcd1234", "2026-11-04", 100_000)
		in.check(t0.Add(time.Hour))
		if got := in.bodies("hooks.test"); len(got) != 1 {
			t.Fatalf("a second alert for the same threshold and period: %v", got)
		}
	})
}

// Spend that jumps over several thresholds between two checks is one alert, for
// the highest, not a burst.
func TestSpendJumpingPastSeveralThresholdsAlertsOnceForTheHighest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-04", 10_200_000)
		in.check(t0)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "HIGH", "100%")

		in.check(t0.Add(time.Minute))
		if got := in.bodies("hooks.test"); len(got) != 1 {
			t.Fatalf("the lower thresholds alerted afterwards: %v", got)
		}
	})
}

// A new day is a new period: the same threshold alerts again.
func TestAThresholdAlertsAgainInTheNextPeriod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-04", 6_000_000)
		in.check(t0)
		in.spent("abcd1234", "2026-11-05", 6_000_000)
		in.check(t0.Add(24 * time.Hour))
		got := in.bodies("hooks.test")
		if len(got) != 2 {
			t.Fatalf("%d alerts over two days, want 2: %v", len(got), got)
		}
		mustContain(t, got[1], "2026-11-05")
	})
}

// A key may set its own percentages and its own soft budget, with no cap at all.
func TestAKeyOverridesThePercentsAndHasASoftBudgetWithoutACap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"monthly_budget_micro":100000000,"soft_budget_percents":[10,95]}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-01", 11_000_000) // 11% of the month's budget; 50 would not have fired
		in.check(t0)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "10%", "monthly soft budget", "2026-11", "$100")
		if strings.Contains(got[0], "cap") {
			t.Errorf("a soft budget with no cap is not a cap:\n%s", got[0])
		}
	})
}

// The install's soft_budget_defaults setting is what a key that sets no
// percentages gets, and it is read each time, so a change takes effect.
func TestAKeyWithNoPercentsUsesTheInstallsDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defaults := []int{25}
		in := newBudgetInstall(t, func(o *alerts.Options) {
			o.Settings = func(context.Context) (alerts.Settings, error) { return alerts.Settings{SoftBudgets: defaults}, nil }
		})
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-04", 3_000_000)
		in.check(t0)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "25%")

		defaults = []int{25, 40} // the setting changes while kman runs
		in.spent("abcd1234", "2026-11-04", 1_500_000)
		in.check(t0.Add(time.Minute))
		if got := in.bodies("hooks.test"); len(got) != 2 {
			t.Fatalf("%d alerts after the setting changed, want 2: %v", len(got), got)
		}
	})
}

// A setting that cannot be read is no reason to warn of nothing.
func TestSettingsThatCannotBeReadFallBackToFiftyEightyAndAHundred(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t, func(o *alerts.Options) {
			o.Settings = func(context.Context) (alerts.Settings, error) { return alerts.Settings{}, context.DeadlineExceeded }
		})
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()
		in.spent("abcd1234", "2026-11-04", 5_000_000)
		in.check(t0)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "50%")
	})
}

// A warning is a lifecycle warning: a channel that turned them off hears
// nothing, and one whose minimum severity is above medium hears only the 100%.
func TestWarningsFollowTheChannelsToggleAndMinimumSeverity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_off", "slack", "off.test", "low", false, "")
		in.channel("ch_high", "slack", "high.test", "high", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-04", 8_500_000)
		in.check(t0)
		in.spent("abcd1234", "2026-11-04", 2_000_000)
		in.check(t0.Add(time.Minute))
		if got := in.bodies("off.test"); len(got) != 0 {
			t.Errorf("a channel with warnings turned off heard %v", got)
		}
		got := in.bodies("high.test")
		if len(got) != 1 {
			t.Fatalf("the high channel heard %d, want 1 (the 100%%): %v", len(got), got)
		}
		mustContain(t, got[0], "100%")
	})
}

// A key that is not active, a canary, or a key with a policy kman cannot read
// has nothing to warn about, and one bad policy does not stop the rest.
func TestOnlyActiveStandardKeysWithReadablePoliciesWarn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("dis00001", "disabled", `{"daily_cap_micro":10000000}`, func(k *store.VirtualKey) { k.Status = store.KeyDisabled })
		in.keyWith("rev00001", "revoked", `{"daily_cap_micro":10000000}`, func(k *store.VirtualKey) { k.Status = store.KeyRevoked })
		in.keyWith("can00001", "canary", `{"daily_cap_micro":10000000}`, func(k *store.VirtualKey) { k.Kind = store.KeyCanary })
		in.keyWith("bad00001", "unreadable", `{"nope":1}`)
		in.keyWith("good0001", "fine", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		for _, id := range []string{"dis00001", "rev00001", "can00001", "bad00001", "good0001"} {
			in.spent(id, "2026-11-04", 9_000_000)
		}
		in.check(t0)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1 (the key that is fine): %v", len(got), got)
		}
		mustContain(t, got[0], "fine", "good0001")
	})
}

// Time is an argument: the producers never read the clock themselves, so what
// counts as today is what Check was told.
func TestCheckCountsTheDayItIsToldNotTheDayItIs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-04", 9_000_000)
		in.check(t0.AddDate(0, 0, 3)) // the 7th: nothing was spent today
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("yesterday's spend counted today: %v", got)
		}
	})
}

// The day is the one in spend_timezone, as the meter counts it.
func TestADayBeginsAtMidnightInTheSpendTimezone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		berlin, err := time.LoadLocation("Europe/Berlin")
		if err != nil {
			t.Skip("no tzdata:", err)
		}
		in := newBudgetInstall(t, func(o *alerts.Options) {
			o.Settings = func(context.Context) (alerts.Settings, error) {
				return alerts.Settings{SoftBudgets: alerts.DefaultSoftBudgets, Location: berlin}, nil
			}
		})
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", "2026-11-05", 9_000_000)
		in.check(time.Date(2026, 11, 4, 23, 30, 0, 0, time.UTC)) // 00:30 on the 5th in Berlin
		if got := in.bodies("hooks.test"); len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
	})
}

// day is the date n days after 2026-11-01, as the spend table labels it.
func day(n int) string { return time.Date(2026, 11, 1+n, 0, 0, 0, 0, time.UTC).Format(time.DateOnly) }

// The projection is the month's spend so far plus the last 7 days' daily average
// times the days left. On the 18th, 12 are left: 49 + 7 × 12 = 133 passes $100.
func TestAMonthOnCourseToPassItsCapAlertsOnceBeforeItDoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"monthly_cap_micro":100000000}`)
		stop := in.run()
		defer stop()

		for d := 10; d <= 16; d++ { // the 11th to the 17th: the 7 days before the 18th
			in.spent("abcd1234", day(d), 7_000_000)
		}
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
		in.check(now)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "MEDIUM", "projected", "monthly cap", "my-app", "$133", "$100", "$49", "$7", "Days left: 12", "2026-11")

		in.check(now.Add(time.Hour))
		in.spent("abcd1234", day(17), 400_000) // more spend, still short of the 50%: one projection alert for the month
		in.check(now.Add(2 * time.Hour))
		if got := in.bodies("hooks.test"); len(got) != 1 {
			t.Fatalf("%d alerts, want the one: %v", len(got), got)
		}
	})
}

func TestAMonthOnCourseToStayUnderItsCapDoesNotAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"monthly_cap_micro":100000000}`)
		stop := in.run()
		defer stop()

		for d := 10; d <= 16; d++ {
			in.spent("abcd1234", day(d), 2_000_000) // 14 + 2 × 12 = 38
		}
		in.check(time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("an alert for a month that stays under: %v", got)
		}
	})
}

// A month that has already passed its limit has had its 100% alert: a
// projection of what has happened would say it twice.
func TestAMonthAlreadyOverItsLimitAlertsForThatAndNotForTheProjection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"monthly_cap_micro":100000000}`)
		stop := in.run()
		defer stop()

		in.spent("abcd1234", day(16), 120_000_000)
		in.check(time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "100%")
		if strings.Contains(got[0], "projected") {
			t.Errorf("a projection of a month that is already over:\n%s", got[0])
		}
	})
}

// The last 7 days may reach back into last month, and a soft budget with no cap
// is what the projection is held to.
func TestTheProjectionReadsBackAcrossTheStartOfTheMonthAndCountsASoftBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"monthly_budget_micro":100000000}`)
		stop := in.run()
		defer stop()

		for d := 24; d <= 30; d++ { // Nov 25 to Dec 1
			in.spent("abcd1234", day(d), 10_000_000)
		}
		// On Dec 2 the month holds Dec 1's 10: 10 + (70 / 7) × 29 days left = 300.
		in.check(time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC))
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "projected", "monthly soft budget", "$300", "$100", "$10", "Days left: 29", "2026-12")
	})
}

// A key about to expire alerts at 7 days, again at 1 day, and when it has
// expired, each once, and says what to type to extend it.
func TestAKeyAboutToExpireAlertsAtSevenDaysOneDayAndWhenExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		expires := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
		in.keyWith("abcd1234", "my-app", `{}`, func(k *store.VirtualKey) { k.ExpiresAt = expires })
		stop := in.run()
		defer stop()

		in.check(expires.Add(-8 * 24 * time.Hour))
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("an alert 8 days out: %v", got)
		}
		in.check(expires.Add(-6 * 24 * time.Hour))
		in.check(expires.Add(-5 * 24 * time.Hour)) // once
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts inside 7 days, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "MEDIUM", "my-app", "abcd1234", "expires within 7 days", "2026-11-20 12:00 UTC", "kman key update abcd1234 --ttl 90d")

		in.check(expires.Add(-20 * time.Hour))
		got = in.bodies("hooks.test")
		if len(got) != 2 {
			t.Fatalf("%d alerts inside a day, want 2: %v", len(got), got)
		}
		mustContain(t, got[1], "HIGH", "expires within a day")

		in.check(expires.Add(time.Minute))
		got = in.bodies("hooks.test")
		if len(got) != 3 {
			t.Fatalf("%d alerts once expired, want 3: %v", len(got), got)
		}
		mustContain(t, got[2], "HIGH", "has expired", "key_expired")

		in.check(expires.Add(48 * time.Hour))
		if got := in.bodies("hooks.test"); len(got) != 3 {
			t.Fatalf("an alert after the last: %v", got)
		}
	})
}

// A key made for two hours is not warned about a week ahead, though it is
// warned that it has expired, and a key whose expiry is extended is warned again.
func TestAShortLivedKeyIsNotWarnedAWeekAheadAndAnExtendedKeyIsWarnedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		created := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
		expires := created.Add(2 * time.Hour)
		in.keyWith("abcd1234", "my-app", `{}`, func(k *store.VirtualKey) { k.CreatedAt, k.ExpiresAt = created, expires })
		stop := in.run()
		defer stop()

		in.check(created.Add(10 * time.Minute))
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("a 2-hour key warned a week ahead: %v", got)
		}
		in.check(expires.Add(time.Minute))
		if got := in.bodies("hooks.test"); len(got) != 1 {
			t.Fatalf("%d alerts once expired, want 1: %v", len(got), got)
		}

		later := expires.Add(30 * 24 * time.Hour)
		err := in.store.Do(context.Background(), func(tx *store.Tx) error {
			k, err := tx.Key("abcd1234")
			if err != nil {
				return err
			}
			k.ExpiresAt = later
			_, err = tx.UpdateKey(k)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		in.check(later.Add(-3 * 24 * time.Hour))
		if got := in.bodies("hooks.test"); len(got) != 2 {
			t.Fatalf("%d alerts after the extension ran down, want 2: %v", len(got), got)
		}
	})
}

// A disabled key is not in use, so its expiry is no news.
func TestADisabledKeysExpiryIsNoWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		expires := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
		in.keyWith("abcd1234", "my-app", `{}`, func(k *store.VirtualKey) { k.ExpiresAt, k.Status = expires, store.KeyDisabled })
		stop := in.run()
		defer stop()
		in.check(expires.Add(-time.Hour))
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("a disabled key warned: %v", got)
		}
	})
}

// RunWatch checks once as it starts, and then every WatchEvery, with the time
// from the service's clock; it ends with its context.
func TestRunWatchChecksAtStartAndEveryInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		in := newBudgetInstall(t, func(o *alerts.Options) {
			o.Now = func() time.Time { return t0.Add(time.Since(start)) } // 12:00 on 2026-11-04, running
			o.WatchEvery = 5 * time.Minute
		})
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("abcd1234", "my-app", `{"daily_cap_micro":10000000}`)
		in.spent("abcd1234", "2026-11-04", 5_500_000)
		stop := in.run()
		defer stop()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { in.svc.RunWatch(ctx); close(done) }()

		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 1 {
			t.Fatalf("%d alerts at start, want 1 (the 50%%): %v", len(got), got)
		}

		in.spent("abcd1234", "2026-11-04", 3_000_000) // 85%
		time.Sleep(4 * time.Minute)
		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 1 {
			t.Fatalf("a check before its interval: %v", got)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 2 {
			t.Fatalf("%d alerts after an interval, want 2: %v", len(got), got)
		}

		cancel()
		<-done
		in.spent("abcd1234", "2026-11-04", 3_000_000)
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 2 {
			t.Fatalf("a check after RunWatch ended: %v", got)
		}
	})
}

// The setting can be "none": no percentages by default. A key that sets its own
// still warns.
func TestTheInstallsDefaultsCanBeNoneAndAKeysOwnPercentsStillWarn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newBudgetInstall(t, func(o *alerts.Options) {
			o.Settings = func(context.Context) (alerts.Settings, error) { return alerts.Settings{SoftBudgets: []int{}}, nil }
		})
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.keyWith("aaaa0001", "plain", `{"daily_cap_micro":10000000}`)
		in.keyWith("bbbb0001", "own", `{"daily_cap_micro":10000000,"soft_budget_percents":[60]}`)
		stop := in.run()
		defer stop()
		in.spent("aaaa0001", "2026-11-04", 9_000_000)
		in.spent("bbbb0001", "2026-11-04", 9_000_000)
		in.check(t0)
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1 (the key with its own percentages): %v", len(got), got)
		}
		mustContain(t, got[0], "own", "60%")
	})
}
