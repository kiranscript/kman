package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// The states and resolutions of an incident, as the API shows them.
const (
	IncidentOpen         = store.IncidentOpen
	IncidentAcknowledged = store.IncidentAcknowledged
	IncidentResolved     = store.IncidentResolved

	ResolutionConfirmed     = store.ResolutionConfirmed
	ResolutionFalsePositive = store.ResolutionFalsePositive
	ResolutionExpired       = store.ResolutionExpired
)

// IncidentQuietAfter is how long an unresolved incident goes without a firing
// before it is quiet; a firing on a quiet incident is an escalation.
const IncidentQuietAfter = store.IncidentQuietAfter

// Limits on listing incidents.
const (
	defaultIncidentLimit = 50
	maxIncidentLimit     = 500
	// maxShownDetections is how many detections an incident's detail gives out.
	maxShownDetections = 100
	// recordedFirings is detect.MaxDetectionsPerIncident: an incident's first
	// rows are every firing, and the rows after them are only escalations.
	recordedFirings = 100
	// maxNewestDetections bounds the rows read after the first recordedFirings.
	// Escalations are rare (a new rule, a higher severity, or a firing after an
	// hour of quiet), so only an incident left open for over a year holds more,
	// and then its oldest escalations past the first rows are not read.
	maxNewestDetections = 10_000
)

// Incident is the open case for one key: the detections of its rules, grouped,
// until someone resolves it or it expires. A key has at most one unresolved.
type Incident struct {
	ID             int64      `json:"id" doc:"A plain integer, as alerts and commands name it."`
	KeyID          string     `json:"key_id"`
	KeyName        string     `json:"key_name,omitempty"`
	State          string     `json:"state" doc:"open, acknowledged or resolved."`
	Quiet          bool       `json:"quiet,omitempty" doc:"Nothing has fired for an hour. Display only: the incident is still unresolved, and a new firing on a quiet incident alerts again."`
	Severity       string     `json:"severity" doc:"low, medium, high or critical: the highest of its detections."`
	Rules          []string   `json:"rules" doc:"The rules that have fired, in the order they first did."`
	DetectionCount int64      `json:"detection_count" doc:"Every detection raised, repeats included. The detail lists at most 100 of them, every escalation included when there are 100 or fewer."`
	OpenedAt       time.Time  `json:"opened_at"`
	LastFiredAt    time.Time  `json:"last_fired_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy     string     `json:"resolved_by,omitempty" doc:"An admin token's name, or system:detect when it expired."`
	Resolution     string     `json:"resolution,omitempty" doc:"confirmed, false_positive or expired."`
}

// IncidentQuery picks incidents.
type IncidentQuery struct {
	Open   bool   `json:"-" query:"open" doc:"Only incidents that are not resolved."`
	Key    string `json:"-" query:"key" doc:"Only this key's incidents, by key id."`
	Before int64  `json:"-" query:"before" doc:"Only incidents with an id below this: the cursor for paging."`
	Limit  int    `json:"-" query:"limit" doc:"The most incidents to return, 1 to 500. Default 50."`
}

// IncidentList is a page of incidents, newest first.
type IncidentList struct {
	Incidents []Incident `json:"incidents"`
	More      bool       `json:"more" doc:"There are older incidents beyond this page: pass the last id as before."`
}

// IncidentRef names an incident.
type IncidentRef struct {
	ID int64 `json:"-" path:"id" doc:"The incident's id."`
}

// IncidentDetail is an incident with what it recorded and what was done about it.
type IncidentDetail struct {
	Incident
	Detections []IncidentDetection    `json:"detections" doc:"What fired, oldest first, at most 100: the detections that escalated the incident (the first and the newest, if there are more), then the oldest and the newest of the rest. detection_count says how many there were."`
	Actions    []AuditEntry           `json:"actions" doc:"The audit entries that carry the incident's id: its opening and escalations by system:detect, and what operators did about it. With the detections, the timeline."`
	Candidates []KnownSourceCandidate `json:"known_source_candidates" doc:"The places the detections saw, which this was me can add as known sources. None for a canary."`
	// AddressesWithheld says an address or network was left out.
	AddressesWithheld bool `json:"addresses_withheld,omitempty" doc:"A client address or network was left out of the evidence because expose_client_addresses is off."`
}

// IncidentDetection is one firing of a rule.
type IncidentDetection struct {
	ID       int64            `json:"id"`
	Time     time.Time        `json:"time"`
	Rule     string           `json:"rule"`
	Severity string           `json:"severity"`
	Evidence IncidentEvidence `json:"evidence"`
}

// IncidentEvidence is what a detection saw. The address and network are
// personal data (ADR 0010), so they are left out unless the
// expose_client_addresses setting is on; everything else always is given.
type IncidentEvidence struct {
	Address         string `json:"address,omitempty" doc:"The client address. Left out unless expose_client_addresses is on."`
	Usual           string `json:"usual,omitempty" doc:"For a new network, the networks the key normally uses, busiest first. Left out unless expose_client_addresses is on."`
	Network         string `json:"network,omitempty" doc:"The network (IPv4 /24, IPv6 /64). Left out unless expose_client_addresses is on."`
	AddressWithheld bool   `json:"address_withheld,omitempty" doc:"There is an address or network, which is left out because expose_client_addresses is off."`
	Country         string `json:"country,omitempty"`
	ASN             int64  `json:"asn,omitempty"`
	ASOrg           string `json:"as_org,omitempty"`
	Client          string `json:"client,omitempty" doc:"What called: the User-Agent reduced to a product and major version."`
	Observed        string `json:"observed,omitempty" doc:"What the rule saw, for a rule that compares: a rate, a spend, a place."`
	Baseline        string `json:"baseline,omitempty" doc:"What the key normally does."`
	Detail          string `json:"detail,omitempty"`
}

// KnownSourceCandidate is a place an incident saw that could become a known
// source.
type KnownSourceCandidate struct {
	Kind  string `json:"kind" doc:"cidr, country, asn or client."`
	Value string `json:"value,omitempty" doc:"The value to add. Empty for a network while expose_client_addresses is off."`
	Label string `json:"label,omitempty"`
	// Withheld marks a network that is not given out, and that this was me will
	// not add while the setting is off.
	Withheld bool `json:"withheld,omitempty"`
}

// ResolveIncidentRequest resolves an incident.
type ResolveIncidentRequest struct {
	ID int64  `json:"-" path:"id" doc:"The incident's id."`
	As string `json:"as,omitempty" doc:"confirmed (the default) or false_positive (false-positive is accepted). A false positive lets what the incident held out of the key's baseline back in, so it is the operator's call. expired is only ever set by kman."`
}

// ThisWasMeRequest says the operator caused the incident.
type ThisWasMeRequest struct {
	ID              int64    `json:"-" path:"id" doc:"The incident's id."`
	AddKnownSources bool     `json:"add_known_sources,omitempty" doc:"Also add the places the incident saw as known sources, so they stop counting as new. Never for a canary."`
	InstallWide     bool     `json:"install_wide,omitempty" doc:"Add them for every key, not just this one. Needs add_known_sources."`
	Kinds           []string `json:"kinds,omitempty" doc:"Add only these kinds: country, asn, client, network. Default all. Needs add_known_sources."`
	Label           string   `json:"label,omitempty" doc:"The label for the known sources. Default says which incident they came from. Needs add_known_sources."`
}

// ThisWasMeResult is the incident, resolved as a false positive, and the known
// sources that were added.
type ThisWasMeResult struct {
	Incident         Incident      `json:"incident"`
	Added            []KnownSource `json:"added" doc:"The known sources added."`
	AlreadyKnown     int           `json:"already_known,omitempty" doc:"Places that were already known sources in that scope, so none was added."`
	WithheldNetworks int           `json:"withheld_networks,omitempty" doc:"Networks that were not added, because adding one would give it out while expose_client_addresses is off."`
}

func incidentDTO(tx *store.Tx, inc store.Incident, now time.Time) (Incident, error) {
	out := Incident{
		ID: inc.ID, KeyID: inc.KeyID, State: inc.State, Quiet: quiet(inc, now), Severity: string(inc.Severity), Rules: inc.Rules(),
		DetectionCount: inc.Detections, OpenedAt: inc.OpenedAt, LastFiredAt: inc.LastFiredAt,
		AcknowledgedAt: timePtr(inc.AcknowledgedAt), AcknowledgedBy: inc.AcknowledgedBy,
		ResolvedAt: timePtr(inc.ResolvedAt), ResolvedBy: inc.ResolvedBy, Resolution: inc.Resolution,
	}
	if out.Rules == nil {
		out.Rules = []string{}
	}
	k, err := tx.Key(inc.KeyID)
	switch {
	case err == nil:
		out.KeyName = k.Name
	case !errors.Is(err, store.ErrNotFound):
		return Incident{}, err
	}
	return out, nil
}

func quiet(inc store.Incident, now time.Time) bool {
	return inc.State != store.IncidentResolved && now.Sub(inc.LastFiredAt) >= IncidentQuietAfter
}

func incidentNotFound(id int64) error {
	return errorf(CodeNotFound, "incident %d does not exist", id)
}

// ListIncidents returns incidents, newest first.
func (s *Service) ListIncidents(ctx context.Context, req IncidentQuery) (IncidentList, error) {
	if req.Limit < 0 || req.Limit > maxIncidentLimit {
		return IncidentList{}, errorf(CodeInvalid, "limit must be from 1 to %d", maxIncidentLimit)
	}
	if req.Before < 0 {
		return IncidentList{}, errorf(CodeInvalid, "before must not be negative")
	}
	limit := req.Limit
	if limit == 0 {
		limit = defaultIncidentLimit
	}
	out := IncidentList{Incidents: []Incident{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		if req.Key != "" {
			if _, err := tx.Key(req.Key); err != nil {
				return fromStore(err, "key "+req.Key)
			}
		}
		rows, err := tx.Incidents(store.IncidentFilter{KeyID: req.Key, UnresolvedOnly: req.Open, Before: req.Before, Limit: limit + 1})
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows, out.More = rows[:limit], true
		}
		now := s.now()
		for _, r := range rows {
			dto, err := incidentDTO(tx, r, now)
			if err != nil {
				return err
			}
			out.Incidents = append(out.Incidents, dto)
		}
		return nil
	})
	return out, err
}

// GetIncident returns an incident with its detections, the actions taken on it
// and the places it could add as known sources. A client address or network in
// the evidence is left out unless expose_client_addresses is on.
func (s *Service) GetIncident(ctx context.Context, req IncidentRef) (IncidentDetail, error) {
	var out IncidentDetail
	err := s.read(ctx, func(tx *store.Tx) error {
		settings, err := loadSettings(tx)
		if err != nil {
			return err
		}
		inc, err := tx.Incident(req.ID)
		if err != nil {
			return fromStore(err, fmt.Sprintf("incident %d", req.ID))
		}
		if out.Incident, err = incidentDTO(tx, inc, s.now()); err != nil {
			return err
		}
		ds, err := readDetections(tx, inc.ID)
		if err != nil {
			return err
		}
		out.Detections = []IncidentDetection{}
		for _, d := range shownDetections(ds, maxShownDetections) {
			ev, withheld := evidenceDTO(d.EvidenceJSON, settings.ExposeClientAddresses)
			out.AddressesWithheld = out.AddressesWithheld || withheld
			out.Detections = append(out.Detections, IncidentDetection{ID: d.ID, Time: d.At, Rule: d.Rule, Severity: string(d.Severity), Evidence: ev})
		}
		audits, err := tx.AuditEntriesForIncident(inc.ID)
		if err != nil {
			return err
		}
		out.Actions = []AuditEntry{}
		for _, e := range audits {
			out.Actions = append(out.Actions, AuditEntry{ID: e.ID, Time: e.TS, Actor: e.Actor, Action: e.Action, TargetKind: e.TargetKind, TargetID: e.TargetID,
				IncidentID: e.IncidentID, Before: rawOrNil(e.BeforeJSON), After: rawOrNil(e.AfterJSON), ClientIP: e.ClientIP})
		}
		k, err := tx.Key(inc.KeyID)
		if err != nil {
			return err
		}
		out.Candidates = []KnownSourceCandidate{}
		if k.Kind != store.KeyCanary {
			out.Candidates = candidates(ds, settings.ExposeClientAddresses)
		}
		return nil
	})
	return out, err
}

// readDetections reads an incident's detection rows, oldest first: the first
// recordedFirings, which are every firing, and the newest maxNewestDetections
// after them, which are escalations.
func readDetections(tx *store.Tx, incidentID int64) ([]store.Detection, error) {
	ds, err := tx.Detections(incidentID, recordedFirings)
	if err != nil || len(ds) < recordedFirings {
		return ds, err
	}
	tail, err := tx.NewestDetections(incidentID, maxNewestDetections)
	if err != nil {
		return nil, err
	}
	last := ds[len(ds)-1].ID
	for _, d := range tail {
		if d.ID > last {
			ds = append(ds, d)
		}
	}
	return ds, nil
}

// shownDetections picks at most limit of an incident's detections (as
// readDetections reads them), oldest first, to show: every one that escalated
// it, and then the oldest and the newest of the rest. Every row past the first
// recordedFirings is an escalation, since detect records nothing else there;
// among the first, a row escalated if it was the first, a new rule, a higher
// severity, or a firing an hour or more after every earlier one. If the
// escalations alone are too many, the first and the newest of them are shown.
func shownDetections(ds []store.Detection, limit int) []store.Detection {
	if len(ds) <= limit {
		return ds
	}
	keep := make([]bool, len(ds))
	var escalations []int
	rules := map[string]bool{}
	var top store.Severity
	var latest time.Time
	for i, d := range ds {
		if i == 0 || i >= recordedFirings || !rules[d.Rule] || d.Severity.Rank() > top.Rank() || d.At.Sub(latest) >= store.IncidentQuietAfter {
			escalations = append(escalations, i)
		}
		rules[d.Rule] = true
		if d.Severity.Rank() > top.Rank() {
			top = d.Severity
		}
		if d.At.After(latest) {
			latest = d.At
		}
	}
	if len(escalations) > limit {
		escalations = append(escalations[:1], escalations[len(escalations)-(limit-1):]...)
	}
	for _, i := range escalations {
		keep[i] = true
	}
	var rest []int
	for i := range ds {
		if !keep[i] {
			rest = append(rest, i)
		}
	}
	room := min(limit-len(escalations), len(rest))
	oldest := (room + 1) / 2
	for _, i := range rest[:oldest] {
		keep[i] = true
	}
	for _, i := range rest[len(rest)-(room-oldest):] {
		keep[i] = true
	}
	out := make([]store.Detection, 0, limit)
	for i, d := range ds {
		if keep[i] {
			out = append(out, d)
		}
	}
	return out
}

// evidenceDTO reads a detection's evidence, leaving out its address and network
// unless expose is set, and says whether it did.
func evidenceDTO(raw string, expose bool) (IncidentEvidence, bool) {
	var ev store.Evidence
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		return IncidentEvidence{}, false
	}
	out := IncidentEvidence{Country: ev.Country, ASN: ev.ASN, ASOrg: ev.ASOrg, Client: ev.Client, Observed: ev.Observed, Baseline: ev.Baseline, Detail: ev.Detail}
	if ev.Address == "" && ev.Network == "" && ev.Usual == "" {
		return out, false
	}
	if !expose {
		out.AddressWithheld = true
		return out, true
	}
	out.Address, out.Network, out.Usual = ev.Address, ev.Network, ev.Usual
	return out, false
}

// candidates are the distinct places the detections saw, in the order first
// seen: a country, an ASN, a client, and the network of an address. A network is
// given out only with expose; without it the candidate says one exists.
func candidates(ds []store.Detection, expose bool) []KnownSourceCandidate {
	out := []KnownSourceCandidate{}
	seen := map[string]bool{}
	add := func(c KnownSourceCandidate) {
		key := c.Kind + "/" + c.Value
		if !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	for _, d := range ds {
		var ev store.Evidence
		if json.Unmarshal([]byte(d.EvidenceJSON), &ev) != nil {
			continue
		}
		if ev.Country != "" {
			add(KnownSourceCandidate{Kind: store.SourceCountry, Value: ev.Country})
		}
		if ev.ASN != 0 {
			add(KnownSourceCandidate{Kind: store.SourceASN, Value: strconv.FormatInt(ev.ASN, 10), Label: ev.ASOrg})
		}
		if ev.Client != "" && ev.Client != "none" && ev.Client != "unknown" {
			add(KnownSourceCandidate{Kind: store.SourceClient, Value: ev.Client})
		}
		network := ev.Network
		if network == "" {
			network = store.NetworkOf(ev.Address)
		}
		if network != "" {
			if expose {
				add(KnownSourceCandidate{Kind: store.SourceCIDR, Value: network})
			} else {
				add(KnownSourceCandidate{Kind: store.SourceCIDR, Withheld: true})
			}
		}
	}
	return out
}

// incidentForKey checks an incident named by a containment command for a key: it
// must exist, be about that key and be unresolved.
func incidentForKey(tx *store.Tx, id int64, keyID string) (store.Incident, error) {
	inc, err := unresolved(tx, id)
	if err != nil {
		return store.Incident{}, err
	}
	if inc.KeyID != keyID {
		return store.Incident{}, errorf(CodeConflict, "incident %d is about key %s, not key %s", id, inc.KeyID, keyID)
	}
	return inc, nil
}

// incidentForCredential is incidentForKey for a command on a credential: the
// incident must be about a key that is on it.
func incidentForCredential(tx *store.Tx, id int64, credentialID string) (store.Incident, error) {
	inc, err := unresolved(tx, id)
	if err != nil {
		return store.Incident{}, err
	}
	k, err := tx.Key(inc.KeyID)
	if err != nil {
		return store.Incident{}, err
	}
	if k.CredentialID != credentialID {
		return store.Incident{}, errorf(CodeConflict, "incident %d is about key %s, which is not on this credential", id, inc.KeyID)
	}
	return inc, nil
}

func unresolved(tx *store.Tx, id int64) (store.Incident, error) {
	inc, err := tx.Incident(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Incident{}, incidentNotFound(id)
		}
		return store.Incident{}, err
	}
	if inc.State == store.IncidentResolved {
		return store.Incident{}, errorf(CodeConflict, "incident %d is already resolved (%s)", id, strings.ReplaceAll(inc.Resolution, "_", " "))
	}
	return inc, nil
}

// resolveIncident ends an unresolved incident and returns the audit entry that
// records it, carrying the incident's id.
func resolveIncident(tx *store.Tx, inc store.Incident, resolution, action, actor string, now time.Time) (audit, error) {
	before, err := incidentDTO(tx, inc, now)
	if err != nil {
		return audit{}, err
	}
	inc.State, inc.Resolution, inc.ResolvedAt, inc.ResolvedBy = store.IncidentResolved, resolution, now, actor
	if inc, err = tx.UpdateIncident(inc); err != nil {
		return audit{}, fromStore(err, fmt.Sprintf("incident %d", inc.ID))
	}
	after, err := incidentDTO(tx, inc, now)
	if err != nil {
		return audit{}, err
	}
	return audit{action: action, kind: "incident", id: strconv.FormatInt(inc.ID, 10), before: before, after: after, incident: inc.ID}, nil
}

// AckIncident moves an open incident to acknowledged: its repeats stop alerting,
// and an escalation still alerts. Acknowledging an acknowledged incident changes
// nothing; a resolved one is a conflict.
func (s *Service) AckIncident(ctx context.Context, req IncidentRef) (Incident, error) {
	c, err := caller(ctx)
	if err != nil {
		return Incident{}, err
	}
	var out Incident
	err = s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		inc, err := unresolved(tx, req.ID)
		if err != nil {
			return nil, err
		}
		before, err := incidentDTO(tx, inc, now)
		if err != nil {
			return nil, err
		}
		if inc.State == store.IncidentAcknowledged {
			out = before
			return nil, nil
		}
		inc.State, inc.AcknowledgedAt, inc.AcknowledgedBy = store.IncidentAcknowledged, now, c.Actor
		if inc, err = tx.UpdateIncident(inc); err != nil {
			return nil, fromStore(err, fmt.Sprintf("incident %d", req.ID))
		}
		if out, err = incidentDTO(tx, inc, now); err != nil {
			return nil, err
		}
		return []audit{{action: "incident.acknowledge", kind: "incident", id: strconv.FormatInt(inc.ID, 10), before: before, after: out, incident: inc.ID}}, nil
	})
	return out, err
}

// resolution reads the --as of a resolve: confirmed by default.
func resolution(as string) (string, error) {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(as)), "-", "_") {
	case "", ResolutionConfirmed:
		return ResolutionConfirmed, nil
	case ResolutionFalsePositive:
		return ResolutionFalsePositive, nil
	}
	return "", errorf(CodeInvalid, "an incident is resolved as confirmed or false_positive, not %q (expired is only set by kman)", as)
}

// ResolveIncident ends an incident as confirmed (a real leak or abuse, whose
// held-out events are dropped) or a false positive. Resolving it the same way
// again changes nothing; another way is a conflict.
func (s *Service) ResolveIncident(ctx context.Context, req ResolveIncidentRequest) (Incident, error) {
	res, err := resolution(req.As)
	if err != nil {
		return Incident{}, err
	}
	c, err := caller(ctx)
	if err != nil {
		return Incident{}, err
	}
	var out Incident
	err = s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		inc, err := tx.Incident(req.ID)
		if err != nil {
			return nil, fromStore(err, fmt.Sprintf("incident %d", req.ID))
		}
		if inc.State == store.IncidentResolved && inc.Resolution == res {
			out, err = incidentDTO(tx, inc, now)
			return nil, err
		}
		if inc, err = unresolved(tx, req.ID); err != nil {
			return nil, err
		}
		e, err := resolveIncident(tx, inc, res, "incident.resolve", c.Actor, now)
		if err != nil {
			return nil, err
		}
		out, _ = e.after.(Incident)
		return []audit{e}, nil
	})
	return out, err
}

var thisWasMeKinds = []string{"country", "asn", "client", "network"}

// ThisWasMe resolves an incident as a false positive and, if asked, adds the
// places it saw as known sources: for the key, or for every key. A canary has no
// known sources, since none ever silences one, so asking for them on a canary's
// incident is a conflict (the operator may still say it was them). A network is
// added only when expose_client_addresses is on, since adding it would give it
// out otherwise.
func (s *Service) ThisWasMe(ctx context.Context, req ThisWasMeRequest) (ThisWasMeResult, error) {
	if !req.AddKnownSources && (req.InstallWide || len(req.Kinds) > 0 || req.Label != "") {
		return ThisWasMeResult{}, errorf(CodeInvalid, "install_wide, kinds and label belong to add_known_sources")
	}
	for _, k := range req.Kinds {
		if !slices.Contains(thisWasMeKinds, k) {
			return ThisWasMeResult{}, errorf(CodeInvalid, "kind %q is not one of country, asn, client or network", k)
		}
	}
	label := req.Label
	if label == "" {
		label = fmt.Sprintf("this was me: incident %d", req.ID)
	} else if err := checkSourceLabel(label); err != nil {
		return ThisWasMeResult{}, err
	}
	c, err := caller(ctx)
	if err != nil {
		return ThisWasMeResult{}, err
	}
	out := ThisWasMeResult{Added: []KnownSource{}}
	err = s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		out = ThisWasMeResult{Added: []KnownSource{}}
		inc, err := tx.Incident(req.ID)
		if err != nil {
			return nil, fromStore(err, fmt.Sprintf("incident %d", req.ID))
		}
		k, err := tx.Key(inc.KeyID)
		if err != nil {
			return nil, err
		}
		if req.AddKnownSources && k.Kind == store.KeyCanary {
			return nil, errorf(CodeConflict, "incident %d is about a canary: a known source never silences a canary, so none is added", req.ID)
		}
		if inc.State == store.IncidentResolved && inc.Resolution != store.ResolutionFalsePositive {
			return nil, errorf(CodeConflict, "incident %d is already resolved (%s)", req.ID, strings.ReplaceAll(inc.Resolution, "_", " "))
		}
		var entries []audit
		if inc.State != store.IncidentResolved {
			e, err := resolveIncident(tx, inc, store.ResolutionFalsePositive, "incident.this_was_me", c.Actor, now)
			if err != nil {
				return nil, err
			}
			out.Incident, _ = e.after.(Incident)
			entries = append(entries, e)
		} else if out.Incident, err = incidentDTO(tx, inc, now); err != nil {
			return nil, err
		}
		if !req.AddKnownSources {
			return entries, nil
		}
		settings, err := loadSettings(tx)
		if err != nil {
			return nil, err
		}
		ds, err := readDetections(tx, inc.ID) // every place it saw, not only those shown
		if err != nil {
			return nil, err
		}
		scope := inc.KeyID
		if req.InstallWide {
			scope = ""
		}
		have, err := tx.KnownSourcesFor(inc.KeyID)
		if err != nil {
			return nil, err
		}
		for _, cand := range candidates(ds, settings.ExposeClientAddresses) {
			kind := cand.Kind
			if kind == store.SourceCIDR {
				kind = "network"
			}
			if len(req.Kinds) > 0 && !slices.Contains(req.Kinds, kind) {
				continue
			}
			if cand.Withheld {
				out.WithheldNetworks++
				continue
			}
			if slices.ContainsFunc(have, func(h store.KnownSource) bool {
				return h.Kind == cand.Kind && h.Value == cand.Value && (h.Scope == "" || h.Scope == scope)
			}) {
				out.AlreadyKnown++
				continue
			}
			lbl := label
			if req.Label == "" && cand.Label != "" {
				lbl = cleanLabel(label + " (" + cand.Label + ")")
			}
			ks, entry, err := createKnownSource(tx, store.KnownSource{Scope: scope, Kind: cand.Kind, Value: cand.Value, Label: lbl, CreatedBy: c.Actor, CreatedAt: now}, inc.ID)
			if err != nil {
				return nil, err
			}
			have = append(have, ks)
			dto, err := knownSourceDTO(tx, ks)
			if err != nil {
				return nil, err
			}
			out.Added = append(out.Added, dto)
			entry.after = dto
			entries = append(entries, entry)
		}
		return entries, nil
	})
	return out, err
}
