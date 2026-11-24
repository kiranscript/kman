package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// Actor is who kman records as having made a change it decided on itself.
const Actor = "system:detect"

// The timings of an incident (SPEC §10), which are not settings.
const (
	// DefaultQuietAfter is how long an incident goes without a firing before it
	// is quiet. Quiet is display only, except that a firing on a quiet incident
	// is an escalation.
	DefaultQuietAfter = store.IncidentQuietAfter
	// DefaultExpireAfter is how long an unacknowledged incident that is not
	// critical may be quiet before it expires.
	DefaultExpireAfter = store.IncidentExpireAfter
	// DefaultSweepEvery is how often the expiry sweep runs.
	DefaultSweepEvery = 15 * time.Minute
)

// MaxDetectionsPerIncident bounds the detection rows kept under one incident. A
// leaked canary used a million times is a million firings, and an incident whose
// timeline is a million rows tells nobody more than one with a counter. Past the
// bound only the incident's counter moves, except that a detection which
// escalates is always recorded.
const MaxDetectionsPerIncident = 100

// Change says what a detection did to its key's incident.
type Change string

// The changes. Opened and Escalated alert; Repeated only counts.
const (
	Opened    Change = "opened"
	Escalated Change = "escalated"
	Repeated  Change = "repeated"
)

// Detection is what a rule raises: it fired for a key, at a severity, with
// evidence. Raising it opens the key's incident or adds to it.
type Detection struct {
	KeyID    string
	Rule     string
	Severity store.Severity
	At       time.Time
	Evidence store.Evidence
	// Silent says the caller alerts for this detection itself. The incident is
	// still opened and audited, but the engine queues no alert: a canary's use
	// has an alert of its own, throttled differently (ADR 0013).
	Silent bool
}

// Outcome is what Raise did.
type Outcome struct {
	// Incident is the key's incident after the detection.
	Incident store.Incident
	Change   Change
	// Detection is the row recorded, zero when a repeat past the bound was only
	// counted.
	Detection store.Detection
	silent    bool
}

// Alerter queues the alert for an incident that opened or escalated, in the
// caller's transaction, so the incident and its alert commit together. alerts
// implements it, and the composition root connects them (detect does not import
// alerts, nor alerts detect).
type Alerter interface {
	// AlertIncident queues the alert; change is "opened" or "escalated", and
	// det is the detection that caused it.
	AlertIncident(tx *store.Tx, change string, inc store.Incident, det store.Detection) error
	// Wake tells the dispatcher there is something to deliver, once a
	// transaction that queued an alert has committed.
	Wake()
}

// Options configures New.
type Options struct {
	Store *store.Store
	// Now is the clock: detect never reads the wall clock itself.
	Now func() time.Time
	// Alerts queues incident alerts. Nil queues none.
	Alerts Alerter
	// QuietAfter, ExpireAfter and SweepEvery default to the constants above.
	QuietAfter, ExpireAfter, SweepEvery time.Duration
	Log                                 *slog.Logger // defaults to discarding
}

// Engine runs the incident state machine: one unresolved incident per key,
// opened and escalated by detections, and expired when nobody looks.
//
// It is the one writer of incidents besides the admin commands an operator
// runs, and it writes as the actor system:detect, with an audit entry for each
// open, escalation and expiry (SPEC §2).
type Engine struct {
	o Options
}

// New returns an Engine.
func New(o Options) *Engine {
	if o.QuietAfter <= 0 {
		o.QuietAfter = DefaultQuietAfter
	}
	if o.ExpireAfter <= 0 {
		o.ExpireAfter = DefaultExpireAfter
	}
	if o.SweepEvery <= 0 {
		o.SweepEvery = DefaultSweepEvery
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Engine{o: o}
}

// Quiet reports whether an unresolved incident has gone without a firing for
// after, as of now. A resolved incident is over, not quiet.
func Quiet(inc store.Incident, now time.Time, after time.Duration) bool {
	return inc.State != store.IncidentResolved && now.Sub(inc.LastFiredAt) >= after
}

// RaiseNow is Raise in its own transaction, which wakes the dispatcher once the
// alert it queued has committed.
func (e *Engine) RaiseNow(ctx context.Context, d Detection) (Outcome, error) {
	var out Outcome
	err := e.o.Store.Do(ctx, func(tx *store.Tx) (err error) {
		out, err = e.Raise(tx, d)
		return err
	})
	if err != nil {
		return Outcome{}, err
	}
	e.wake(out)
	return out, nil
}

// Wake wakes the dispatcher after a transaction in which Raise returned out has
// committed, if the detection queued an alert.
func (e *Engine) Wake(out Outcome) { e.wake(out) }

func (e *Engine) wake(out Outcome) {
	if e.o.Alerts != nil && out.Change != Repeated && !out.silent {
		e.o.Alerts.Wake()
	}
}

// Raise records a detection in the caller's transaction and moves the key's
// incident.
//
//   - With no unresolved incident, one opens.
//   - With one, the detection escalates it when it has a higher severity, a rule
//     the incident has not had, or arrives after the incident went quiet; the
//     incident then takes the highest severity and the new rule.
//   - Otherwise it is a repeat, which only counts and moves the incident's last
//     firing, so that an incident that keeps firing does not go quiet or expire.
//
// An open and an escalation are audited as system:detect and queue an alert
// through the Alerter, unless the detection is Silent. A repeat writes neither.
// The caller wakes the dispatcher after the commit (Wake, or RaiseNow).
func (e *Engine) Raise(tx *store.Tx, d Detection) (Outcome, error) {
	switch {
	case d.KeyID == "":
		return Outcome{}, errors.New("detect: a detection needs a key")
	case d.Rule == "":
		return Outcome{}, errors.New("detect: a detection needs a rule")
	case d.Severity.Rank() == 0:
		return Outcome{}, fmt.Errorf("detect: severity %q is not low, medium, high or critical", d.Severity)
	}
	at := d.At
	if at.IsZero() {
		at = e.o.Now()
	}
	evidence, err := json.Marshal(d.Evidence)
	if err != nil {
		return Outcome{}, fmt.Errorf("detect: marshal evidence: %w", err)
	}
	inc, err := tx.UnresolvedIncident(d.KeyID)
	var change Change
	switch {
	case errors.Is(err, store.ErrNotFound):
		rules, _ := json.Marshal([]string{d.Rule})
		inc, err = tx.CreateIncident(store.Incident{KeyID: d.KeyID, Severity: d.Severity, RulesJSON: string(rules), Detections: 1, OpenedAt: at, LastFiredAt: at})
		if err != nil {
			return Outcome{}, err
		}
		change = Opened
	case err != nil:
		return Outcome{}, err
	default:
		change = Repeated
		rules := inc.Rules()
		newRule := true
		for _, r := range rules {
			if r == d.Rule {
				newRule = false
			}
		}
		if newRule || d.Severity.Rank() > inc.Severity.Rank() || Quiet(inc, at, e.o.QuietAfter) {
			change = Escalated
		}
		if newRule {
			rules = append(rules, d.Rule)
		}
		raw, err := json.Marshal(rules)
		if err != nil {
			return Outcome{}, fmt.Errorf("detect: marshal rules: %w", err)
		}
		inc.RulesJSON = string(raw)
		if d.Severity.Rank() > inc.Severity.Rank() {
			inc.Severity = d.Severity
		}
		inc.Detections++
		if at.After(inc.LastFiredAt) {
			inc.LastFiredAt = at
		}
	}
	out := Outcome{Change: change, silent: d.Silent}
	if change == Repeated && inc.Detections > MaxDetectionsPerIncident {
		// Counted, not recorded.
		out.Incident, err = tx.UpdateIncident(inc)
		return out, err
	}
	row, err := tx.AddDetection(store.Detection{IncidentID: inc.ID, KeyID: d.KeyID, At: at, Rule: d.Rule, Severity: d.Severity, EvidenceJSON: string(evidence)})
	if err != nil {
		return Outcome{}, err
	}
	out.Detection = row
	if change != Repeated && !d.Silent && e.o.Alerts != nil {
		inc.AlertedSeverity = inc.Severity
	}
	if inc, err = tx.UpdateIncident(inc); err != nil {
		return Outcome{}, err
	}
	out.Incident = inc
	if change == Repeated {
		return out, nil
	}
	if err := e.audit(tx, at, "incident."+map[Change]string{Opened: "open", Escalated: "escalate"}[change], inc, d.Rule); err != nil {
		return Outcome{}, err
	}
	if !d.Silent && e.o.Alerts != nil {
		if err := e.o.Alerts.AlertIncident(tx, string(change), inc, row); err != nil {
			return Outcome{}, err
		}
	}
	return out, nil
}

// audit appends an entry for a change the engine made, by system:detect, with
// the incident's id.
func (e *Engine) audit(tx *store.Tx, at time.Time, action string, inc store.Incident, rule string) error {
	after, err := json.Marshal(struct {
		State      string         `json:"state"`
		Severity   store.Severity `json:"severity"`
		Rules      []string       `json:"rules"`
		Rule       string         `json:"rule,omitempty"`
		Resolution string         `json:"resolution,omitempty"`
	}{inc.State, inc.Severity, inc.Rules(), rule, inc.Resolution})
	if err != nil {
		return fmt.Errorf("detect: marshal audit entry: %w", err)
	}
	_, err = tx.AppendAudit(store.AuditEntry{TS: at, Actor: Actor, Action: action, TargetKind: "incident",
		TargetID: strconv.FormatInt(inc.ID, 10), IncidentID: inc.ID, AfterJSON: string(after)})
	return err
}

// sweepBatch is how many incidents one pass of the sweep expires at most.
const sweepBatch = 200

// Sweep expires the incidents nobody looked at: unacknowledged, not critical,
// and without a firing for ExpireAfter. Each is resolved as expired by
// system:detect, with an audit entry carrying its id, and queues no alert. It
// returns how many it expired.
func (e *Engine) Sweep(ctx context.Context) (int, error) {
	now := e.o.Now()
	var n int
	err := e.o.Store.Do(ctx, func(tx *store.Tx) error {
		n = 0
		stale, err := tx.ExpirableIncidents(now.Add(-e.o.ExpireAfter), sweepBatch)
		if err != nil {
			return err
		}
		for _, inc := range stale {
			inc.State, inc.Resolution, inc.ResolvedAt, inc.ResolvedBy = store.IncidentResolved, store.ResolutionExpired, now, Actor
			if inc, err = tx.UpdateIncident(inc); err != nil {
				return err
			}
			if err := e.audit(tx, now, "incident.expire", inc, ""); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// RunSweep sweeps at startup and then every SweepEvery, until ctx ends.
func (e *Engine) RunSweep(ctx context.Context) {
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := e.Sweep(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			e.o.Log.Error("detect: could not expire quiet incidents", "err", err)
		case n > 0:
			e.o.Log.Info("detect: expired incidents nobody acknowledged", "count", n)
		}
		t.Reset(e.o.SweepEvery)
	}
}

// Origin is where a request came from, as the location rules know it: what a
// known source is matched against.
type Origin struct {
	Addr    netip.Addr // the zero Addr for none
	Country string
	ASN     int64
	Client  string
}

// MatchKnownSource returns the known source, among sources, that applies to a
// request from o on a key, and says whether there is one. Sources for another
// key are skipped; a source matches by its kind: a CIDR contains the address, a
// country or a client is equal, an ASN is the same number.
//
// A canary is never matched: no known source silences one (ADR 0013). Whatever
// calls this for a key must say whether it is a canary, so the rule holds
// however a location rule is written.
func MatchKnownSource(sources []store.KnownSource, keyID string, canary bool, o Origin) (store.KnownSource, bool) {
	if canary {
		return store.KnownSource{}, false
	}
	addr := o.Addr
	if addr.IsValid() {
		addr = addr.Unmap()
	}
	for _, s := range sources {
		if s.Scope != "" && s.Scope != keyID {
			continue
		}
		switch s.Kind {
		case store.SourceCIDR:
			if p, err := netip.ParsePrefix(s.Value); err == nil && addr.IsValid() && p.Contains(addr) {
				return s, true
			}
		case store.SourceCountry:
			if o.Country != "" && s.Value == o.Country {
				return s, true
			}
		case store.SourceASN:
			if o.ASN != 0 && s.Value == strconv.FormatInt(o.ASN, 10) {
				return s, true
			}
		case store.SourceClient:
			if o.Client != "" && s.Value == o.Client {
				return s, true
			}
		}
	}
	return store.KnownSource{}, false
}
