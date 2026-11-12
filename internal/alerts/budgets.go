package alerts

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// DefaultSoftBudgets are the percentages of a cap or soft budget that warn for a
// key that sets none, and the install has not changed (SPEC §8).
var DefaultSoftBudgets = []int{50, 80, 100}

// Limits are the amounts a key's spend warnings count from, in microdollars,
// read from its policy. A period's soft budget, where it has one, is what its
// warnings count from; otherwise its cap is. Zero is none.
type Limits struct {
	DailyCapMicro, MonthlyCapMicro       int64
	DailyBudgetMicro, MonthlyBudgetMicro int64
	// Percents are this key's own thresholds, or none to use the install's.
	Percents []int
}

// a period's reference amount and what to call it.
func (l Limits) daily() (int64, string) {
	if l.DailyBudgetMicro > 0 {
		return l.DailyBudgetMicro, "daily soft budget"
	}
	return l.DailyCapMicro, "daily cap"
}

func (l Limits) monthly() (int64, string) {
	if l.MonthlyBudgetMicro > 0 {
		return l.MonthlyBudgetMicro, "monthly soft budget"
	}
	return l.MonthlyCapMicro, "monthly cap"
}

// Check is one turn of the warnings that spend and keys raise (SPEC §8, §9): a
// key that has spent a threshold's share of a cap or soft budget, a month's
// spend that is on course to pass one, and a key about to expire. Each alerts
// once per period, and survives a restart, because the record that it was sent
// commits with the alert in one transaction. now is the time the check is for:
// it is an argument, and the clock is never read here.
//
// Spend is what the meter has flushed, which is at most its flush interval
// behind. Nothing is sent for a key that is not active, or is a canary.
func (s *Service) Check(ctx context.Context, now time.Time) error {
	cfg := s.settings(ctx)
	err := s.o.Store.Do(ctx, func(tx *store.Tx) error {
		keys, err := tx.Keys()
		if err != nil {
			return err
		}
		w := newWindow(now, cfg.Location)
		spend, err := tx.SpendSince(w.from)
		if err != nil {
			return err
		}
		byKey := map[string]map[string]int64{}
		for _, r := range spend {
			if byKey[r.KeyID] == nil {
				byKey[r.KeyID] = map[string]int64{}
			}
			byKey[r.KeyID][r.Day] += r.CostMicro
		}
		for _, k := range keys {
			if k.Status != store.KeyActive || k.Kind != store.KeyStandard {
				continue
			}
			if err := s.checkExpiry(tx, k, now); err != nil {
				return err
			}
			if s.o.Limits != nil {
				lim, err := s.o.Limits(k.PolicyJSON)
				if err != nil {
					s.o.Log.Warn("alerts: a key's policy could not be read, so it has no spend warnings", "key", k.ID, "err", err)
				} else if err := s.checkSpend(tx, k, lim, cfg.SoftBudgets, w, byKey[k.ID], now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Wake()
	return nil
}

// RunWatch checks spend and expiry as it starts and then every WatchEvery, with
// the service's clock, until ctx ends. A check that fails is logged, and the
// next one tries again.
func (s *Service) RunWatch(ctx context.Context) {
	t := time.NewTicker(s.o.WatchEvery)
	defer t.Stop()
	for {
		if err := s.Check(ctx, s.o.Now()); err != nil && ctx.Err() == nil {
			s.o.Log.Error("alerts: could not check spend and expiry", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// settings reads the install's settings. Where they are missing or cannot be
// read, the defaults apply. An empty soft_budget_defaults is not missing: it is
// the operator's "none", and only a key's own percentages warn.
func (s *Service) settings(ctx context.Context) Settings {
	cfg := Settings{SoftBudgets: DefaultSoftBudgets}
	if s.o.Settings != nil {
		got, err := s.o.Settings(ctx)
		if err != nil {
			s.o.Log.Warn("alerts: the settings could not be read, so the defaults apply", "err", err)
		} else {
			cfg.SoftBudgets, cfg.Location = got.SoftBudgets, got.Location
		}
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	return cfg
}

// window is the day and month a check is for, in the spend timezone, and the
// days a projection reads.
type window struct {
	day, month string
	// last7 are the 7 days before today, which a projection averages. A day
	// that is not over would make the average low.
	last7    []string
	daysLeft int    // days in the month after today
	from     string // the earliest day a check reads
}

func newWindow(now time.Time, loc *time.Location) window {
	y, m, d := now.In(loc).Date()
	day, month := store.SpendLabels(now, loc) // the labels the meter wrote spend under
	w := window{day: day, month: month, from: month + "-01"}
	for i := 7; i >= 1; i-- {
		earlier, _ := store.SpendLabels(time.Date(y, m, d-i, 12, 0, 0, 0, loc), loc)
		w.last7 = append(w.last7, earlier)
	}
	w.from = min(w.from, w.last7[0])
	w.daysLeft = time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day() - d
	return w
}

// checkSpend raises the thresholds of one key's daily and monthly amounts.
func (s *Service) checkSpend(tx *store.Tx, k store.VirtualKey, lim Limits, defaults []int, w window, days map[string]int64, now time.Time) error {
	percents := lim.Percents
	if len(percents) == 0 {
		percents = defaults
	}
	var month int64
	for day, c := range days {
		if strings.HasPrefix(day, w.month) {
			month += c
		}
	}
	dayLimit, dayWhat := lim.daily()
	if err := s.crossings(tx, k, w.day, dayWhat, days[w.day], dayLimit, percents, now); err != nil {
		return err
	}
	monthLimit, monthWhat := lim.monthly()
	if err := s.crossings(tx, k, w.month, monthWhat, month, monthLimit, percents, now); err != nil {
		return err
	}
	return s.projection(tx, k, w, days, month, monthLimit, monthWhat, now)
}

// marked is a warning recorded as sent (MarkBudgetAlert) by the producer that is
// about to queue it.
type marked struct {
	keyID, period string
	threshold     int
}

// notifyMarked queues a, and takes back its marks when no channel received it:
// a warning is recorded as sent only once something has been sent, so that a
// channel added afterwards still hears of what is owed (SPEC §9: never loses an
// alert).
func (s *Service) notifyMarked(tx *store.Tx, a Alert, marks ...marked) error {
	n, err := s.enqueue(tx, a)
	if err != nil || n > 0 {
		return err
	}
	for _, m := range marks {
		if err := tx.UnmarkBudgetAlert(m.keyID, m.period, m.threshold); err != nil {
			return err
		}
	}
	return nil
}

// crossings queues the alert for the highest threshold that spent has newly
// reached of limit, and records every threshold it has reached, so that none of
// them alerts again in this period.
func (s *Service) crossings(tx *store.Tx, k store.VirtualKey, period, what string, spent, limit int64, percents []int, now time.Time) error {
	if limit <= 0 {
		return nil
	}
	newest := 0
	var marks []marked
	for _, p := range percents {
		if !reached(spent, limit, p) {
			continue
		}
		first, err := tx.MarkBudgetAlert(k.ID, period, p, now)
		if err != nil {
			return err
		}
		if first {
			marks = append(marks, marked{k.ID, period, p})
			newest = max(newest, p)
		}
	}
	if newest == 0 {
		return nil
	}
	sev := Medium
	if newest >= 100 {
		sev = High
	}
	a := Alert{
		Kind: "budget", Severity: sev, Warning: true, KeyID: k.ID, KeyName: k.Name, Time: now,
		Title: fmt.Sprintf("%s has used %d%% of its %s", label(k), newest, what),
		Evidence: []Fact{
			{"Period", period},
			{"Spent", dollars(spent)},
			{"Limit", fmt.Sprintf("%s (%s)", dollars(limit), what)},
		},
		Commands: []string{"kman key show " + k.ID},
	}
	return s.notifyMarked(tx, a, marks...)
}

// projectionThreshold is what a month's projection warning is recorded under in
// the place of a percentage: no threshold is 0.
const projectionThreshold = 0

// projection queues the alert that the month is on course to pass limit, once
// per month: the month's spend so far, plus the daily average of the last 7 days
// for each day left. A month that has passed limit already has had its
// threshold alerts, and says nothing more.
func (s *Service) projection(tx *store.Tx, k store.VirtualKey, w window, days map[string]int64, month, limit int64, what string, now time.Time) error {
	if limit <= 0 || month >= limit {
		return nil
	}
	var recent int64
	for _, d := range w.last7 {
		recent += days[d]
	}
	avg := recent / int64(len(w.last7))
	projected := satAdd(month, satMul(avg, int64(w.daysLeft)))
	if projected <= limit {
		return nil
	}
	first, err := tx.MarkBudgetAlert(k.ID, w.month, projectionThreshold, now)
	if err != nil || !first {
		return err
	}
	return s.notifyMarked(tx, Alert{
		Kind: "budget_projection", Severity: Medium, Warning: true, KeyID: k.ID, KeyName: k.Name, Time: now,
		Title: fmt.Sprintf("%s is projected to pass its %s", label(k), what),
		Evidence: []Fact{
			{"Period", w.month},
			{"Projected", dollars(projected)},
			{"Limit", fmt.Sprintf("%s (%s)", dollars(limit), what)},
			{"Spent so far", dollars(month)},
			{"Daily average, last 7 days", dollars(avg)},
			{"Days left", strconv.Itoa(w.daysLeft)},
		},
		Commands: []string{"kman key show " + k.ID},
	}, marked{k.ID, w.month, projectionThreshold})
}

// satAdd and satMul keep a projection from wrapping around on an absurd spend.
func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func satMul(a, b int64) int64 {
	if a != 0 && b > math.MaxInt64/a {
		return math.MaxInt64
	}
	return a * b
}

func label(k store.VirtualKey) string {
	if k.Name != "" {
		return k.Name
	}
	return k.ID
}

// reached reports whether spent is at least percent% of limit, without
// overflowing on an amount near the largest.
func reached(spent, limit int64, percent int) bool {
	if spent <= 0 || limit <= 0 || percent <= 0 {
		return false
	}
	aHi, aLo := bits.Mul64(uint64(spent), 100)
	bHi, bLo := bits.Mul64(uint64(limit), uint64(percent))
	return aHi > bHi || (aHi == bHi && aLo >= bLo)
}

// dollars prints microdollars as dollars with no more digits than it takes:
// $10, $8.50, $0.025.
func dollars(micro int64) string {
	whole, frac := micro/1_000_000, micro%1_000_000
	if frac == 0 {
		return fmt.Sprintf("$%d", whole)
	}
	digits := strings.TrimRight(fmt.Sprintf("%06d", frac), "0")
	if len(digits) == 1 {
		digits += "0"
	}
	return fmt.Sprintf("$%d.%s", whole, digits)
}

// expiryWarnings are how many days before its expiry a key is warned, and at 0,
// when it has expired and its requests are refused with key_expired.
var expiryWarnings = []int{7, 1, 0}

// checkExpiry queues the warning for the nearest expiry threshold a key has
// newly reached. A warning is recorded under the expiry it is about, so a key
// whose expiry is extended is warned again as the new one nears. A key is not
// warned of a threshold that was already past when it was made: one that lives
// for two hours has no week's notice to give.
func (s *Service) checkExpiry(tx *store.Tx, k store.VirtualKey, now time.Time) error {
	if k.ExpiresAt.IsZero() {
		return nil
	}
	period := "expiry:" + k.ExpiresAt.UTC().Format(time.RFC3339)
	newest, found := 0, false
	var marks []marked
	for _, days := range expiryWarnings {
		at := k.ExpiresAt.Add(-time.Duration(days) * 24 * time.Hour)
		if now.Before(at) || (days > 0 && !at.After(k.CreatedAt)) {
			continue
		}
		first, err := tx.MarkBudgetAlert(k.ID, period, days, now)
		if err != nil {
			return err
		}
		if first {
			marks = append(marks, marked{k.ID, period, days})
			newest, found = days, true // the list falls, so the last new one is the nearest
		}
	}
	if !found {
		return nil
	}
	a := Alert{
		Kind: "key_expiry", Severity: High, Warning: true, KeyID: k.ID, KeyName: k.Name, Time: now,
		Evidence: []Fact{{"Expires", k.ExpiresAt.UTC().Format("2006-01-02 15:04 MST")}},
		Commands: []string{"kman key show " + k.ID, "kman key update " + k.ID + " --ttl 90d"},
	}
	switch newest {
	case 0:
		a.Title = label(k) + " has expired"
		a.Evidence = append(a.Evidence, Fact{"Effect", "its requests are refused with key_expired"})
	case 1:
		a.Title = label(k) + " expires within a day"
	default:
		a.Severity = Medium
		a.Title = fmt.Sprintf("%s expires within %d days", label(k), newest)
	}
	return s.notifyMarked(tx, a, marks...)
}
