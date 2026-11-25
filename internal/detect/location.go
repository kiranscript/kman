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
//   - it is a standard key: a canary's use is its own critical rule, and a canary's
//     place is never new;
//   - the key's learning period, which starts at its first forwarded request and
//     is the policy's learning_period, or the install's setting, was over at the
//     time of the request: a key that was only refused has not started one, and the
//     places of the request that started it are the baseline. After learning the
//     first place of any kind is raised too: a key used only from private
//     addresses has no country or ASN, and a thief abroad is exactly that;
//   - the key's policy has not turned the rule off;
//   - no known source covers the place (MatchKnownPlace): a CIDR containing the
//     request's address covers all of it, and a country, ASN or client only a new
//     place of its own kind.
//
// A refused request counts, so an address lock that stops a leak still says that
// the key turned up. The places of one request that are new in several ways are
// raised together and alert once (RaiseGroup). The work is bounded: the usual
// places are read once per kind for the batch, and past
// MaxDetectionGroupsPerKeyPerBatch requests of one key, or
// MaxDetectionGroupsPerBatch of all keys, in a batch a request is raised only for
// a rule that none of its key's earlier ones raised, or when it fires on an
// incident gone quiet. The rest are counted on the incident.
func (e *Engine) ObservePlaces(tx *store.Tx, rows []store.EventRow, fresh []store.NewPlace) error {
	var keys []string
	byKey := map[string][]store.NewPlace{}
	freshOf := map[string][]store.NewPlace{} // every new place of a key, watched or not
	for _, p := range fresh {
		freshOf[p.KeyID] = append(freshOf[p.KeyID], p)
		if _, ok := ruleOf(p.Kind); !ok {
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
	batchGroups := 0 // the groups raised for every key of the batch so far
	for _, id := range keys {
		if err := e.observeKey(tx, id, rows, byKey[id], freshOf[id], install, &batchGroups); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) observeKey(tx *store.Tx, id string, rows []store.EventRow, cands, fresh []store.NewPlace, install time.Duration, batchGroups *int) error {
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
	// The places that are new, in the order of their events, by event. The time of
	// a place is its earliest event's, and the baseline is the key's first
	// forwarded request and what came before it. The store keeps times in
	// milliseconds, so an event's time is compared at that precision.
	learned := start.Add(period)
	var order []int
	byEvent := map[int][]store.NewPlace{}
	for _, p := range cands {
		r, _ := ruleOf(p.Kind)
		at := p.At.Truncate(time.Millisecond)
		if !pol.RuleEnabled(r.rule) || !at.After(start) || at.Before(learned) {
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
	usual := map[string]usualPlaces{}
	raised := map[string]bool{} // the rules raised for this key in this batch
	groups, dropped := 0, 0
	var unraised int64 // the new places past the bounds, which only the counter counts
	var lastUnraised time.Time
	var lastFired time.Time // the key's incident's last firing, counted ones included
	for _, i := range order {
		row := rows[i]
		addr, _ := netip.ParseAddr(row.ClientIP)
		origin := Origin{Addr: addr, Country: row.Country, ASN: row.ASN, Client: row.Client}
		var ds []Detection
		capped := false
		for _, p := range byEvent[i] {
			if _, known := MatchKnownPlace(sources, id, false, p.Kind, origin); known {
				continue
			}
			// Past a bound a place is only counted, unless it fires on an incident
			// gone quiet, which is an escalation (a batch can span an hour when
			// the writer catches up).
			full := groups >= MaxDetectionGroupsPerKeyPerBatch || *batchGroups >= e.o.MaxDetectionGroupsPerBatch
			at := p.At.Truncate(time.Millisecond)
			if r, _ := ruleOf(p.Kind); full && raised[r.rule] && at.Sub(lastFired) < e.o.QuietAfter {
				capped = true
				unraised++
				if at.After(lastUnraised) {
					lastUnraised = at
				}
				if at.After(lastFired) {
					lastFired = at
				}
				continue
			}
			d, err := e.locationDetection(tx, row, p, fresh, learned, usual)
			if err != nil {
				return err
			}
			ds = append(ds, d)
		}
		if len(ds) == 0 {
			if capped {
				dropped++
			}
			continue
		}
		groups++
		*batchGroups++
		// Highest severity first, so that a group's alert names the one that matters.
		slices.SortStableFunc(ds, func(a, b Detection) int { return b.Severity.Rank() - a.Severity.Rank() })
		outs, err := e.RaiseGroup(tx, ds)
		if err != nil {
			return err
		}
		if inc := outs[len(outs)-1].Incident; inc.LastFiredAt.After(lastFired) {
			lastFired = inc.LastFiredAt
		}
		for j, out := range outs {
			raised[ds[j].Rule] = true
			if out.Change != Repeated && !ds[j].Silent && e.o.Alerts != nil {
				e.wakePending.Store(true)
			}
		}
	}
	if unraised > 0 {
		// A rule raised for the key in this batch is why a place was not raised,
		// so the key has an unresolved incident to count it on.
		inc, err := tx.UnresolvedIncident(id)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return err
		default:
			inc.Detections += unraised
			if lastUnraised.After(inc.LastFiredAt) {
				inc.LastFiredAt = lastUnraised
			}
			if _, err := tx.UpdateIncident(inc); err != nil {
				return err
			}
		}
	}
	if dropped > 0 {
		e.o.Log.Warn("detect: a batch held more new places than detections are raised for; the rest were counted on the key's incident and not raised", "key", id, "requests", dropped)
	}
	return nil
}

// usualPlaces is a key's usual places of one kind, read once for a batch.
type usualPlaces struct {
	places []store.PlaceRow
	total  int
}

// MaxDetectionGroupsPerKeyPerBatch bounds the requests of one key that a single
// batch of events raises detections for, and MaxDetectionGroupsPerBatch those of
// every key together, so that a thief spraying new addresses, with one key or
// with many, cannot make the writer's transaction, which every event waits on,
// slow. Past either bound a request of a key is raised only for a rule that no
// earlier request of that key in the batch raised, so each key's incident still
// opens and learns every rule that fired. The rest are added to the incident's
// counter and not recorded, as repeats past MaxDetectionsPerIncident are not.
// The groups of a batch are so at most MaxDetectionGroupsPerBatch, plus one per
// rule for each key in it.
const (
	MaxDetectionGroupsPerKeyPerBatch = MaxDetectionsPerIncident
	MaxDetectionGroupsPerBatch       = 1000
)

// locationDetection is the detection for one new place of one request: what is
// new and what the key normally uses.
func (e *Engine) locationDetection(tx *store.Tx, row store.EventRow, p store.NewPlace, fresh []store.NewPlace, learned time.Time, cache map[string]usualPlaces) (Detection, error) {
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
	up, ok := cache[p.Kind]
	if !ok {
		var except []string // the places this batch added, which are what is being compared
		for _, f := range fresh {
			if f.KeyID == p.KeyID && f.Kind == p.Kind {
				except = append(except, f.Value)
			}
		}
		// What a key normally uses is what it had when it finished learning, so that
		// a place an earlier detection added does not count as usual. A key that had
		// none then is compared with all it has.
		var err error
		up.places, up.total, err = tx.UsualPlaces(p.KeyID, p.Kind, except, learned, usualShown)
		if err == nil && up.total == 0 {
			up.places, up.total, err = tx.UsualPlaces(p.KeyID, p.Kind, except, time.Time{}, usualShown)
		}
		if err != nil {
			return Detection{}, err
		}
		cache[p.Kind] = up
	}
	usual, total := up.places, up.total
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
