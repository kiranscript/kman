package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kiranscript/kman/internal/admin"
)

// defaultIncidentsLimit is how many incidents kman incidents lists, and --follow
// starts from.
const defaultIncidentsLimit = 20

// defaultFollowEvery is how often --follow asks kman what changed.
const defaultFollowEvery = 2 * time.Second

// followWindow is how many of the newest incidents --follow watches.
const followWindow = 200

func cmdIncidents(ctx context.Context, c *call) error {
	fs := c.flags("incidents [--open] [--key <name or key id>] [--limit n] [--follow] [--json]\n\n"+
		"Lists incidents, the newest first: the open case kman keeps for a key when a detection rule fires, with its state, severity, the rules that fired and how many detections it has. "+
		"A key has at most one unresolved incident. An incident is open until someone acknowledges it (kman incident ack) and resolved by kman incident resolve, "+
		"this-was-me, or a containment command given --incident. It is quiet after an hour without a firing, and an unacknowledged one quiet for 7 days expires; "+
		"acknowledged and critical (canary) incidents never do. "+
		"With --follow it prints the newest incidents, oldest first, and then a line each time an incident opens, escalates (a higher severity, a new rule, or a firing after it went quiet), is acknowledged or is resolved, until interrupted. "+
		"A repeat of a rule the incident already has only counts, and is not a change.", true)
	open := fs.Bool("open", false, "only incidents that are not resolved")
	key := fs.String("key", "", "only this key's incidents, by name or key id")
	limit := fs.Int("limit", defaultIncidentsLimit, "how many incidents to list, from 1 to 500; with --follow, how many to start from")
	follow := fs.Bool("follow", false, "keep printing changes until interrupted")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	if *limit < 1 || *limit > 500 {
		return errors.New("--limit: want a number from 1 to 500")
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	req := admin.IncidentQuery{Open: *open, Limit: *limit}
	if *key != "" {
		k, err := resolveKey(ctx, cl, *key)
		if err != nil {
			return c.explain(err, l)
		}
		req.Key = k.ID
	}
	if *follow {
		return incidentsFollow(ctx, c, cl, l, req)
	}
	list, err := cl.ListIncidents(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	if len(list.Incidents) == 0 {
		if *open {
			fmt.Fprintln(c.env.Stdout, "No incidents are open.")
		} else {
			fmt.Fprintln(c.env.Stdout, "No incidents.")
		}
		return nil
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKEY\tSTATE\tSEVERITY\tRULES\tDETECTIONS\tOPENED\tLAST FIRED")
	for _, i := range list.Incidents {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", i.ID, incidentKey(i), incidentState(i), i.Severity, incidentRules(i),
			i.DetectionCount, when(&i.OpenedAt), when(&i.LastFiredAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if list.More {
		fmt.Fprintln(c.env.Stdout, "\nThere are older incidents: kman incidents --limit <n> lists more.")
	}
	return nil
}

// incidentKey is an incident's key, by name and id.
func incidentKey(i admin.Incident) string {
	if i.KeyName == "" {
		return cleanDashboard(i.KeyID)
	}
	return cleanDashboard(i.KeyName) + " (" + cleanDashboard(i.KeyID) + ")"
}

// incidentState is an incident's state: a resolved one with how it ended, and an
// unresolved one that is quiet, as such.
func incidentState(i admin.Incident) string {
	switch {
	case i.State == admin.IncidentResolved:
		return "resolved (" + strings.ReplaceAll(cleanDashboard(i.Resolution), "_", " ") + ")"
	case i.Quiet:
		return cleanDashboard(i.State) + " (quiet)"
	}
	return cleanDashboard(i.State)
}

func incidentRules(i admin.Incident) string {
	rules := make([]string, len(i.Rules))
	for n, r := range i.Rules {
		rules[n] = cleanDashboard(r)
	}
	return strings.Join(rules, ", ")
}

// incidentSignature is what --follow reports a change in: not the counter,
// since a repeat is not news.
func incidentSignature(i admin.Incident) string {
	return strings.Join([]string{i.State, i.Severity, i.Resolution, strings.Join(i.Rules, ",")}, "|")
}

// firedAfterQuiet says an unresolved incident fired again after going quiet
// since it was last seen as old. detect counts that as an escalation and alerts,
// though it changes no state, severity or rule. Its rule is the one detect
// applies: the firing's time against the last one before it, which old holds,
// since --follow keeps the newest copy of each incident it saw.
func firedAfterQuiet(old, i admin.Incident) bool {
	return old.State != admin.IncidentResolved && i.LastFiredAt.Sub(old.LastFiredAt) >= admin.IncidentQuietAfter
}

// incidentsFollow prints the incidents the query picks, oldest first, and then a
// line for each change as it is found, by asking kman now and then. Interrupting
// it, which ends ctx, is how it is meant to end; a server that cannot be
// reached is an error.
func incidentsFollow(ctx context.Context, c *call, cl *client, l login, req admin.IncidentQuery) error {
	enc := json.NewEncoder(c.env.Stdout)
	print := func(i admin.Incident) error {
		if c.json {
			return enc.Encode(i)
		}
		_, err := fmt.Fprintf(c.env.Stdout, "%s  #%d  %s  %s  %s  %s  (%d %s)\n", when(&i.LastFiredAt), i.ID, incidentKey(i), i.Severity, incidentState(i),
			incidentRules(i), i.DetectionCount, plural(i.DetectionCount, "detection", "detections"))
		return err
	}
	first, err := cl.ListIncidents(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	seen := map[int64]admin.Incident{}
	for _, i := range slices.Backward(first.Incidents) {
		seen[i.ID] = i
		if err := print(i); err != nil {
			return err
		}
	}
	every := c.env.FollowEvery
	if every <= 0 {
		every = defaultFollowEvery
	}
	watch := req
	watch.Limit = followWindow
	// What the wider window already holds is not news: only a change from here on is.
	base, err := cl.ListIncidents(ctx, watch)
	if err != nil {
		return c.explain(err, l)
	}
	for _, i := range base.Incidents {
		if _, ok := seen[i.ID]; !ok {
			seen[i.ID] = i
		}
	}
	timer := time.NewTimer(every)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		list, err := cl.ListIncidents(ctx, watch)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return c.explain(err, l)
		}
		current := map[int64]bool{}
		for _, i := range slices.Backward(list.Incidents) {
			current[i.ID] = true
			old, ok := seen[i.ID]
			seen[i.ID] = i // the newest, so the next firing is measured from the last one
			if ok && incidentSignature(old) == incidentSignature(i) && !firedAfterQuiet(old, i) {
				continue
			}
			if err := print(i); err != nil {
				return err
			}
		}
		// With --open an incident that is resolved leaves the list; it is still news.
		if req.Open {
			var gone []int64
			for id, old := range seen {
				if !current[id] && old.State != admin.IncidentResolved {
					gone = append(gone, id)
				}
			}
			sort.Slice(gone, func(a, b int) bool { return gone[a] < gone[b] })
			for _, id := range gone {
				d, err := cl.GetIncident(ctx, admin.IncidentRef{ID: id})
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return c.explain(err, l)
				}
				seen[id] = d.Incident
				if err := print(d.Incident); err != nil {
					return err
				}
			}
		}
		timer.Reset(every)
	}
}

func cmdIncident(ctx context.Context, c *call) error {
	sub, err := c.sub("<show|ack|resolve|this-was-me> ...", "show", "ack", "resolve", "this-was-me")
	if err != nil {
		return err
	}
	switch sub {
	case "show":
		return incidentShow(ctx, c)
	case "ack":
		return incidentAck(ctx, c)
	case "resolve":
		return incidentResolve(ctx, c)
	}
	return incidentThisWasMe(ctx, c)
}

// incidentID reads an incident id argument.
func incidentID(arg string) (int64, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%q is not an incident id: use the number kman incidents shows", arg)
	}
	return id, nil
}

func incidentShow(ctx context.Context, c *call) error {
	fs := c.flags("incident show <id> [--json]\n\nShows an incident: its key, state, severity and rules, each detection with what it saw, and the actions taken, which are its timeline. "+
		"It also lists the places the incident saw, which kman incident this-was-me can add as known sources.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	id, err := incidentID(pos[0])
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	d, err := cl.GetIncident(ctx, admin.IncidentRef{ID: id})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(d)
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	line := func(name, value string) {
		if value != "" {
			fmt.Fprintf(w, "%s:\t%s\n", name, value)
		}
	}
	line("incident", strconv.FormatInt(d.ID, 10))
	line("key", incidentKey(d.Incident))
	line("state", incidentState(d.Incident))
	line("severity", d.Severity)
	line("rules", incidentRules(d.Incident))
	line("detections", strconv.FormatInt(d.DetectionCount, 10))
	line("opened", when(&d.OpenedAt))
	line("last fired", when(&d.LastFiredAt))
	if d.AcknowledgedAt != nil {
		line("acknowledged", when(d.AcknowledgedAt)+" by "+cleanDashboard(d.AcknowledgedBy))
	}
	if d.ResolvedAt != nil {
		line("resolved", when(d.ResolvedAt)+" by "+cleanDashboard(d.ResolvedBy))
	}
	w.Flush()
	fmt.Fprintln(c.env.Stdout, "\nTimeline:")
	for _, item := range incidentTimeline(d) {
		fmt.Fprintf(c.env.Stdout, "  %s  %s\n", item.at.UTC().Format("2006-01-02T15:04:05Z"), item.text)
	}
	if shown := int64(len(d.Detections)); d.DetectionCount > shown {
		fmt.Fprintf(c.env.Stdout, "  (%d more detections are not listed)\n", d.DetectionCount-shown)
	}
	if len(d.Candidates) > 0 {
		fmt.Fprintf(c.env.Stdout, "\nPlaces it saw: %s.\nIf this was you: kman incident this-was-me %d --add-known-sources\n", candidateText(d.Candidates), d.ID)
	}
	if d.AddressesWithheld {
		fmt.Fprintln(c.env.Stdout, "\n"+addressesWithheld)
	}
	return nil
}

type timelineItem struct {
	at   time.Time
	text string
}

// incidentTimeline merges an incident's detections and the actions taken on it,
// by time: detections before actions at the same moment.
func incidentTimeline(d admin.IncidentDetail) []timelineItem {
	var items []timelineItem
	for _, det := range d.Detections {
		items = append(items, timelineItem{det.Time, fmt.Sprintf("%s fired (%s)%s", cleanDashboard(det.Rule), det.Severity, evidenceText(det.Evidence))})
	}
	for _, a := range d.Actions {
		items = append(items, timelineItem{a.Time, actionText(a)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	return items
}

func evidenceText(e admin.IncidentEvidence) string {
	var parts []string
	if e.Address != "" {
		parts = append(parts, "from "+cleanDashboard(e.Address))
	}
	if e.Network != "" {
		parts = append(parts, "network "+cleanDashboard(e.Network))
	}
	if e.Country != "" {
		parts = append(parts, cleanDashboard(e.Country))
	}
	if e.ASN != 0 {
		as := "AS" + strconv.FormatInt(e.ASN, 10)
		if e.ASOrg != "" {
			as += " " + cleanDashboard(e.ASOrg)
		}
		parts = append(parts, as)
	}
	if e.Client != "" {
		parts = append(parts, cleanDashboard(e.Client))
	}
	if e.Observed != "" || e.Baseline != "" {
		parts = append(parts, "observed "+orDash(cleanDashboard(e.Observed))+", baseline "+orDash(cleanDashboard(e.Baseline)))
	}
	if e.Usual != "" {
		parts = append(parts, "usual networks "+cleanDashboard(e.Usual))
	}
	if e.Detail != "" {
		parts = append(parts, cleanDashboard(e.Detail))
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, ", ")
}

// actionText is an audit entry on an incident's timeline, in words.
func actionText(a admin.AuditEntry) string {
	by := " by " + cleanDashboard(a.Actor)
	switch a.Action {
	case "incident.open":
		return "opened" + by
	case "incident.escalate":
		return "escalated" + by
	case "incident.acknowledge":
		return "acknowledged" + by
	case "incident.resolve":
		return "resolved" + by
	case "incident.this_was_me":
		return "marked this was me, resolved as a false positive" + by
	case "incident.expire":
		return "expired: nobody acknowledged it" + by
	case "known_source.create":
		return "known source " + cleanDashboard(a.TargetID) + " added" + by
	}
	return cleanDashboard(a.Action) + " " + cleanDashboard(a.TargetKind) + " " + cleanDashboard(a.TargetID) + by
}

func candidateText(cs []admin.KnownSourceCandidate) string {
	var parts []string
	for _, c := range cs {
		switch {
		case c.Withheld:
			parts = append(parts, "a network (not shown)")
		case c.Label != "":
			parts = append(parts, cleanDashboard(c.Kind)+" "+cleanDashboard(c.Value)+" ("+cleanDashboard(c.Label)+")")
		default:
			parts = append(parts, cleanDashboard(c.Kind)+" "+cleanDashboard(c.Value))
		}
	}
	return strings.Join(parts, ", ")
}

func incidentAck(ctx context.Context, c *call) error {
	fs := c.flags("incident ack <id> [--json]\n\nAcknowledges an incident: its repeats stop alerting, and an escalation (a higher severity or a new rule) still does.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	id, err := incidentID(pos[0])
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.AckIncident(ctx, admin.IncidentRef{ID: id})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Acknowledged incident %d.\n", got.ID)
	return nil
}

func incidentResolve(ctx context.Context, c *call) error {
	fs := c.flags("incident resolve <id> [--as false-positive|confirmed] [--json]\n\nEnds an incident. confirmed (the default) says it was real: what it held out of the key's baseline is dropped. "+
		"false-positive says it was not: that is the operator's call, and kman incident this-was-me also offers to add its places as known sources. "+
		"Rotating or revoking a key, or a credential compromise, given --incident, resolves it as confirmed.", true)
	as := fs.String("as", "confirmed", "how it ended: confirmed or false-positive")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	id, err := incidentID(pos[0])
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.ResolveIncident(ctx, admin.ResolveIncidentRequest{ID: id, As: *as})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Resolved incident %d as %s.\n", got.ID, resolutionText(got.Resolution))
	return nil
}

// resolutionText says how an incident ended, after "as".
func resolutionText(r string) string {
	if r == admin.ResolutionFalsePositive {
		return "a false positive"
	}
	return cleanDashboard(r)
}

func incidentThisWasMe(ctx context.Context, c *call) error {
	fs := c.flags("incident this-was-me <id> [--add-known-sources] [--install-wide] [--kinds country,asn,client,network] [--label <text>] [--json]\n\n"+
		"Says you caused the incident: it is resolved as a false positive. With --add-known-sources the places it saw (its countries, ASNs, clients and networks) are also added as known sources, "+
		"so they stop counting as new: for the key, or for every key with --install-wide; --kinds picks which. On a terminal, without --add-known-sources, kman shows the places and asks. "+
		"A network is added only with the expose_client_addresses setting on. A known source never silences a canary, so none is added for a canary's incident.", true)
	add := fs.Bool("add-known-sources", false, "also add the places the incident saw as known sources")
	wide := fs.Bool("install-wide", false, "add them for every key, not just this one")
	kinds := fs.String("kinds", "", "add only these kinds, separated by commas: country, asn, client, network")
	label := fs.String("label", "", "the label for the known sources (default: says which incident they came from)")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	id, err := incidentID(pos[0])
	if err != nil {
		return err
	}
	options := *wide || *kinds != "" || *label != ""
	if options && !*add && !c.env.Interactive {
		return errors.New("--install-wide, --kinds and --label belong to --add-known-sources: add that flag")
	}
	req := admin.ThisWasMeRequest{ID: id, AddKnownSources: *add, InstallWide: *wide, Label: *label}
	if *kinds != "" {
		req.Kinds = strings.Split(*kinds, ",")
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	var detail admin.IncidentDetail
	// The detail names what could be added: it is shown, and asked about on a terminal.
	if !c.json {
		if detail, err = cl.GetIncident(ctx, admin.IncidentRef{ID: id}); err != nil {
			return c.explain(err, l)
		}
	}
	if !*add && !c.json && c.env.Interactive && len(detail.Candidates) > 0 {
		scope := "for key " + incidentKey(detail.Incident)
		if *wide {
			scope = "for every key"
		}
		fmt.Fprintf(c.env.Stdout, "Incident %d saw: %s.\nAdd them as known sources %s, so they stop counting as new? [y/N] ", id, candidateText(detail.Candidates), scope)
		answer, _ := bufio.NewReader(c.env.Stdin).ReadString('\n')
		fmt.Fprintln(c.env.Stdout)
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			req.AddKnownSources = true
		}
	}
	if !req.AddKnownSources {
		// Declined (or a canary's incident, which is never asked about): the options
		// for adding belong to adding, and the server refuses them without it.
		req.InstallWide, req.Kinds, req.Label = false, nil, ""
	}
	got, err := cl.ThisWasMe(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	w := c.env.Stdout
	fmt.Fprintf(w, "Resolved incident %d as a false positive: this was you.\n", id)
	if len(got.Added) > 0 {
		scope := "for key " + incidentKey(detail.Incident)
		if got.Added[0].Scope == "" {
			scope = "for every key"
		}
		fmt.Fprintf(w, "Added %d known %s %s:\n", len(got.Added), plural(int64(len(got.Added)), "source", "sources"), scope)
		for _, k := range got.Added {
			fmt.Fprintf(w, "  %d  %s %s  %s\n", k.ID, cleanDashboard(k.Kind), cleanDashboard(k.Value), cleanDashboard(k.Label))
		}
	}
	if got.AlreadyKnown > 0 {
		fmt.Fprintf(w, "%d %s already known.\n", got.AlreadyKnown, plural(int64(got.AlreadyKnown), "place was", "places were"))
	}
	if got.WithheldNetworks > 0 {
		fmt.Fprintf(w, "%d %s not added: adding it would show it, and client addresses are off. kman settings set expose_client_addresses true turns them on.\n",
			got.WithheldNetworks, plural(int64(got.WithheldNetworks), "network was", "networks were"))
	}
	switch {
	case !req.AddKnownSources && len(detail.Candidates) > 0:
		fmt.Fprintf(w, "To stop what it saw counting as new, run: kman incident this-was-me %d --add-known-sources [--install-wide]\n", id)
	case len(detail.Candidates) == 0 && slices.Contains(detail.Rules, "canary"):
		fmt.Fprintln(w, "A known source never silences a canary, so none is offered.")
	}
	return nil
}

func cmdKnownSource(ctx context.Context, c *call) error {
	sub, err := c.sub("<add|list|rm> ...", "add", "list", "rm")
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return knownSourceAdd(ctx, c)
	case "list":
		return knownSourceList(ctx, c)
	}
	return knownSourceRm(ctx, c)
}

func knownSourceAdd(ctx context.Context, c *call) error {
	fs := c.flags("known-source add <cidr|country|asn|client> <value> --label <text> [--key <name or key id>] [--json]\n\n"+
		"Says a place is legitimate: use from it never counts as a new location. It is labelled, and applies to every key, or to one with --key. "+
		"cidr is a network such as 203.0.113.0/24, no wider than an IPv4 /16 or an IPv6 /32, or one address; country is an ISO code such as DE; asn is a number such as 3320 or AS3320; "+
		"client is what kman records for a caller, such as curl/8 (none and unknown are refused). A known source never silences a canary.", true)
	label := fs.String("label", "", "what it is, such as \"CI runners\" (required)")
	key := fs.String("key", "", "apply it to this key only, by name or key id (default: every key)")
	pos, err := c.parse(fs, 2, 2)
	if err != nil {
		return err
	}
	if *label == "" {
		fmt.Fprintln(c.env.Stderr, "kman: known-source add: --label is required: say what the place is")
		fs.Usage()
		return errReported
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	req := admin.AddKnownSourceRequest{Kind: pos[0], Value: pos[1], Label: *label}
	if *key != "" {
		k, err := resolveKey(ctx, cl, *key)
		if err != nil {
			return c.explain(err, l)
		}
		req.Key = k.ID
	}
	got, err := cl.AddKnownSource(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Added known source %d: %s %s (%s) for %s.\n", got.ID, cleanDashboard(got.Kind), cleanDashboard(got.Value), cleanDashboard(got.Label), sourceScope(got))
	return nil
}

func sourceScope(k admin.KnownSource) string {
	switch {
	case k.Scope == "":
		return "every key"
	case k.KeyName != "":
		return "key " + cleanDashboard(k.KeyName) + " (" + cleanDashboard(k.Scope) + ")"
	}
	return "key " + cleanDashboard(k.Scope)
}

func knownSourceList(ctx context.Context, c *call) error {
	fs := c.flags("known-source list [--key <name or key id>] [--json]\n\nLists the known sources, the ones for every key first. With --key, only those that apply to that key.", true)
	key := fs.String("key", "", "only those that apply to this key, by name or key id")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	var req admin.KnownSourceQuery
	if *key != "" {
		k, err := resolveKey(ctx, cl, *key)
		if err != nil {
			return c.explain(err, l)
		}
		req.Key = k.ID
	}
	list, err := cl.ListKnownSources(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	if len(list.KnownSources) == 0 {
		fmt.Fprintln(c.env.Stdout, "No known sources. Add one with kman known-source add <cidr|country|asn|client> <value> --label <text>.")
		return nil
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAPPLIES TO\tKIND\tVALUE\tLABEL\tADDED BY\tADDED")
	for _, k := range list.KnownSources {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", k.ID, sourceScope(k), cleanDashboard(k.Kind), cleanDashboard(k.Value), cleanDashboard(k.Label), cleanDashboard(k.CreatedBy), when(&k.CreatedAt))
	}
	return w.Flush()
}

func knownSourceRm(ctx context.Context, c *call) error {
	fs := c.flags("known-source rm <id> [--json]\n\nRemoves a known source: use from that place counts as new again.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(pos[0], 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("%q is not a known source id: use the number kman known-source list shows", pos[0])
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.RemoveKnownSource(ctx, admin.KnownSourceRef{ID: id})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Removed known source %d: %s %s (%s).\n", got.ID, cleanDashboard(got.Kind), cleanDashboard(got.Value), cleanDashboard(got.Label))
	return nil
}
