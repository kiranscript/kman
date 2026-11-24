package detect

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/store"
)

// The location rules (SPEC §10, #89): after a key's learning period, its first
// use from a country, an ASN, a network or a client it has never been used from
// is a detection. Each is named as a policy's detect names it, and has the
// severity of the issue that defined it, which is not a setting.
const (
	RuleNewCountry = "new_country"
	RuleNewASN     = "new_asn"
	RuleNewNetwork = "new_network"
	RuleNewClient  = "new_client"
)

// locationRule is how one kind of place is watched.
type locationRule struct {
	kind     string
	rule     string
	severity store.Severity
}

var locationRules = []locationRule{
	{store.PlaceCountry, RuleNewCountry, store.SeverityHigh},
	{store.PlaceASN, RuleNewASN, store.SeverityMedium},
	{store.PlaceNetwork, RuleNewNetwork, store.SeverityLow},
	{store.PlaceClient, RuleNewClient, store.SeverityMedium},
}

func ruleOf(kind string) (locationRule, bool) {
	for _, r := range locationRules {
		if r.kind == kind {
			return r, true
		}
	}
	return locationRule{}, false
}

// usualShown is how many of a key's usual places of a kind a detection names.
const usualShown = 3

// maxText bounds each piece of text a detection puts in evidence.
const maxText = 64

// ObservePlaces is the event writer's observer (events.Observer): it runs in the
// transaction that stored a batch of events, with the places the batch gave
// their keys for the first time, and raises the location rules for them. A
// place is new exactly once, in the transaction that records it, so the
// detection commits with the place or neither does.
//
// A place is raised only when all of these hold:
//   - the key already had a place of that kind, so there was something to compare
//     it with (a key first seen with a GeoIP database has no countries to differ
//     from, and a client was not recorded before kman learned to);
//   - it is a standard key: a canary's use is its own critical rule, and a canary's
//     place is never new;
//   - the key's learning period, which starts at its first forwarded request and
//     is the policy's learning_period, or the install's setting, is over at the
//     time of the request: a key that was only refused has not started one;
//   - the key's policy has not turned the rule off;
//   - no known source covers the request: its address, country, ASN or client.
//
// A refused request counts, so an address lock that stops a leak still says that
// the key turned up. The places of one request that are new in several ways are
// raised together and alert once (RaiseGroup).
func (e *Engine) ObservePlaces(tx *store.Tx, rows []store.EventRow, fresh []store.NewPlace) error {
	var keys []string
	byKey := map[string][]store.NewPlace{}
	for _, p := range fresh {
		if _, ok := ruleOf(p.Kind); !ok || p.Prior == 0 {
			continue
		}
		if _, ok := byKey[p.KeyID]; !ok {
			keys = append(keys, p.KeyID)
		}
		byKey[p.KeyID] = append(byKey[p.KeyID], p)
	}
	if len(keys) == 0 {
		return nil
	}
	install := store.DefaultLearningPeriod
	if d, err := tx.LearningPeriod(); err != nil {
		e.o.Log.Error("detect: could not read the learning_period setting; using the default", "err", err)
	} else {
		install = d
	}
	for _, id := range keys {
		if err := e.observeKey(tx, id, rows, byKey[id], fresh, install); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) observeKey(tx *store.Tx, id string, rows []store.EventRow, cands, fresh []store.NewPlace, install time.Duration) error {
	key, err := tx.Key(id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil // events of a key that is gone have no incident to join
	case err != nil:
		return err
	case key.Kind == store.KeyCanary:
		return nil
	}
	pol, err := policy.Parse(key.PolicyJSON)
	if err != nil {
		e.o.Log.Error("detect: a key's policy cannot be read; its location rules use the defaults", "key", id, "err", err)
		pol = policy.Policy{}
	}
	start, started, err := tx.LearningStart(id)
	if err != nil || !started {
		return err
	}
	period := install
	if d, ok := pol.LearningPeriodOverride(); ok {
		period = d
	}
	// The places that are new, in the order of their events, by event.
	var order []int
	byEvent := map[int][]store.NewPlace{}
	for _, p := range cands {
		r, _ := ruleOf(p.Kind)
		if !pol.RuleEnabled(r.rule) || p.At.Before(start.Add(period)) {
			continue
		}
		if _, ok := byEvent[p.Event]; !ok {
			order = append(order, p.Event)
		}
		byEvent[p.Event] = append(byEvent[p.Event], p)
	}
	if len(order) == 0 {
		return nil
	}
	sources, err := tx.KnownSourcesFor(id)
	if err != nil {
		return err
	}
	for _, i := range order {
		row := rows[i]
		addr, _ := netip.ParseAddr(row.ClientIP)
		if _, known := MatchKnownSource(sources, id, false, Origin{Addr: addr, Country: row.Country, ASN: row.ASN, Client: row.Client}); known {
			continue
		}
		var ds []Detection
		for _, p := range byEvent[i] {
			d, err := e.locationDetection(tx, row, p, fresh, start.Add(period))
			if err != nil {
				return err
			}
			ds = append(ds, d)
		}
		// Highest severity first, so that a group's alert names the one that matters.
		slices.SortStableFunc(ds, func(a, b Detection) int { return b.Severity.Rank() - a.Severity.Rank() })
		outs, err := e.RaiseGroup(tx, ds)
		if err != nil {
			return err
		}
		for j, out := range outs {
			if out.Change != Repeated && !ds[j].Silent && e.o.Alerts != nil {
				e.wakePending.Store(true)
			}
		}
	}
	return nil
}

// locationDetection is the detection for one new place of one request: what is
// new and what the key normally uses.
func (e *Engine) locationDetection(tx *store.Tx, row store.EventRow, p store.NewPlace, fresh []store.NewPlace, learned time.Time) (Detection, error) {
	r, _ := ruleOf(p.Kind)
	ev := store.Evidence{
		Address: cleanText(row.ClientIP), Network: store.NetworkOf(row.ClientIP), Country: cleanText(row.Country),
		ASN: row.ASN, ASOrg: cleanText(row.ASOrg), Client: cleanText(row.Client),
	}
	if row.Kind == "blocked" {
		ev.Detail = "the request was refused"
		if code := cleanText(row.BlockedCode); code != "" {
			ev.Detail += " (" + code + ")"
		}
	}
	var except []string // the places this batch added, which are what is being compared
	for _, f := range fresh {
		if f.KeyID == p.KeyID && f.Kind == p.Kind {
			except = append(except, f.Value)
		}
	}
	// What a key normally uses is what it had when it finished learning, so that
	// a place an earlier detection added does not count as usual. A key that had
	// none then (a kind that began to be recorded later) is compared with all it has.
	usual, total, err := tx.UsualPlaces(p.KeyID, p.Kind, except, learned, usualShown)
	if err == nil && total == 0 {
		usual, total, err = tx.UsualPlaces(p.KeyID, p.Kind, except, time.Time{}, usualShown)
	}
	if err != nil {
		return Detection{}, err
	}
	switch p.Kind {
	case store.PlaceCountry:
		ev.Observed = "new country " + cleanText(p.Value)
	case store.PlaceASN:
		ev.Observed = "new ASN AS" + cleanText(p.Value)
		if org := cleanText(p.Label); org != "" {
			ev.Observed += " (" + org + ")"
			ev.ASOrg = org
		}
	case store.PlaceNetwork:
		ev.Observed = "new network" // the value is an address: Network has it, and the API withholds it
	case store.PlaceClient:
		ev.Observed = "new client " + cleanText(p.Value)
	}
	switch {
	case total == 0:
		ev.Baseline = "no usual places recorded"
	case p.Kind == store.PlaceNetwork:
		ev.Baseline = fmt.Sprintf("%d usual %s", total, plural(total, "network", "networks"))
		ev.Usual = usualText(p.Kind, usual, total)
	default:
		ev.Baseline = usualText(p.Kind, usual, total)
	}
	return Detection{KeyID: p.KeyID, Rule: r.rule, Severity: r.severity, At: row.Time, Evidence: ev}, nil
}

// usualText lists a key's busiest places of a kind, with how many requests each
// has had, and how many more there are.
func usualText(kind string, places []store.PlaceRow, total int) string {
	parts := make([]string, 0, len(places)+1)
	for _, p := range places {
		name := cleanText(p.Value)
		if kind == store.PlaceASN {
			name = "AS" + name
			if org := cleanText(p.Label); org != "" {
				name += " " + org
			}
		}
		parts = append(parts, fmt.Sprintf("%s (%s %s)", name, strconv.FormatInt(p.Count, 10), plural(p.Count, "request", "requests")))
	}
	if more := total - len(places); more > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", more))
	}
	return strings.Join(parts, ", ")
}

func plural[N int | int64](n N, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// cleanText makes text a database file or a client could have supplied safe to
// show: control characters become spaces, runs of space one, and it is cut at
// maxText characters.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxText {
		s = string(r[:maxText]) + "…"
	}
	return s
}

// AfterCommit wakes the alert dispatcher if the transaction that has just
// committed queued an incident alert (events.Observer).
func (e *Engine) AfterCommit() {
	if e.wakePending.Swap(false) && e.o.Alerts != nil {
		e.o.Alerts.Wake()
	}
}
