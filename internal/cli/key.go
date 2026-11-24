package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/kirank55/kman/internal/admin"
)

func cmdKey(ctx context.Context, c *call) error {
	sub, err := c.sub("<create|list|show|places|clients|update|disable|enable|revoke|rotate> ...", "create", "list", "show", "places", "clients", "update", "disable", "enable", "revoke", "rotate")
	if err != nil {
		return err
	}
	switch sub {
	case "create":
		return keyCreate(ctx, c)
	case "list":
		return keyList(ctx, c)
	case "show":
		return keyShow(ctx, c)
	case "places":
		return keyPlaces(ctx, c)
	case "clients":
		return keyClients(ctx, c)
	case "disable", "enable", "revoke":
		return keyLifecycle(ctx, c, sub)
	case "rotate":
		return keyRotate(ctx, c)
	}
	return keyUpdate(ctx, c)
}

// keyLifecycle is key disable, enable or revoke: one key, by name or key id,
// stopped or restored from the next request. Disable and revoke take --incident,
// the incident they are done for.
func keyLifecycle(ctx context.Context, c *call, action string) error {
	usage := "key " + action + " <name or key id> [--json]"
	if action != "enable" {
		usage = "key " + action + " <name or key id> [--incident <id>] [--json]"
		switch action {
		case "disable":
			usage += "\n\nWith --incident the action is recorded on that incident's timeline, and the incident stays open: disabling is a pause, and kman incident resolve ends it."
		default:
			usage += "\n\nWith --incident the action is recorded on that incident's timeline and the incident is resolved as confirmed."
		}
	}
	fs := c.flags(usage, true)
	var incident *int64
	if action != "enable" {
		incident = fs.Int64("incident", 0, "the incident this is done for, by id: it must be about this key and unresolved")
	}
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveKey(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	var got admin.Key
	var done string
	switch action {
	case "enable":
		got, err = cl.EnableKey(ctx, admin.KeyRef{ID: k.ID})
		done = "Enabled"
	case "revoke":
		got, err = cl.RevokeKey(ctx, admin.ContainKeyRequest{ID: k.ID, IncidentID: *incident})
		done = "Revoked"
	default:
		got, err = cl.DisableKey(ctx, admin.ContainKeyRequest{ID: k.ID, IncidentID: *incident})
		done = "Disabled"
	}
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "%s key %s.\n", done, keyLabel(got))
	if incident != nil && *incident != 0 {
		incidentNote(c, *incident, action)
	}
	return nil
}

// incidentNote says what a containment command did to its incident.
func incidentNote(c *call, id int64, action string) {
	if action == "disable" {
		fmt.Fprintf(c.env.Stdout, "Recorded on incident %d, which stays open: kman incident resolve %d ends it.\n", id, id)
		return
	}
	fmt.Fprintf(c.env.Stdout, "Resolved incident %d as confirmed.\n", id)
}

// ttlFlag parses --ttl into seconds, and policyValue checks --policy is JSON
// before anything is sent. The server has the last word on both.
func ttlFlag(v string) (int64, error) {
	n, err := parseSeconds(v)
	if err != nil {
		return 0, fmt.Errorf("--ttl: %w", err)
	}
	return n, nil
}

func policyValue(v string) (json.RawMessage, error) {
	if !json.Valid([]byte(v)) {
		return nil, errors.New("--policy: not valid JSON")
	}
	return json.RawMessage(v), nil
}

func tagFlag(fs *flag.FlagSet) *[]string {
	tags := &[]string{}
	fs.Func("tag", "a tag; repeat it for several", func(v string) error {
		*tags = append(*tags, v)
		return nil
	})
	return tags
}

func keyCreate(ctx context.Context, c *call) error {
	const usage = "key create [<name>] --upstream <upstream> [--credential <credential>] [--ttl <duration>] [--note <text>] [--tag <tag>]... [--policy <json>] [--json]"
	fs := c.flags(usage, true)
	upstream := fs.String("upstream", "", "the upstream's name or id (required)")
	credential := fs.String("credential", "", "the credential's name or id, when the upstream has more than one")
	ttl := fs.String("ttl", "", "how long the key stays valid, such as 90d or 36h (default: the default_key_ttl setting)")
	note := fs.String("note", "", "free text about the key")
	policy := fs.String("policy", "", "the key's policy as JSON (default: the empty policy)")
	tags := tagFlag(fs)
	pos, err := c.parse(fs, 0, 1)
	if err != nil {
		return err
	}
	if *upstream == "" {
		fs.Usage()
		return errReported
	}
	req := admin.CreateKeyRequest{Upstream: *upstream, Credential: *credential, Note: *note, Tags: *tags}
	if len(pos) == 1 {
		req.Name = pos[0]
	}
	if *ttl != "" {
		n, err := ttlFlag(*ttl)
		if err != nil {
			return err
		}
		req.TTLSeconds = &n
	}
	if *policy != "" {
		if req.Policy, err = policyValue(*policy); err != nil {
			return err
		}
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	created, err := cl.CreateKey(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(created)
	}
	k := created.Key
	secret := string(created.Secret)
	fmt.Fprintf(c.env.Stdout, "Created key %s on upstream %s, credential %s. It expires %s.\nIts secret, shown only this once:\n\n  %s\n\n",
		keyLabel(k), k.Upstream, k.Credential, when(&k.ExpiresAt), secret)
	if created.SDKEnv != "" {
		fmt.Fprintf(c.env.Stdout, "  export %s=%s\n\n", created.SDKEnv, secret)
	}
	if created.SDKBasePath != "" {
		fmt.Fprintf(c.env.Stdout, "Point the SDK's base URL at kman's proxy listener plus %s.\n", created.SDKBasePath)
	}
	return nil
}

// keyLabel names a key in a sentence.
func keyLabel(k admin.Key) string {
	if k.Name == "" {
		return k.ID
	}
	return fmt.Sprintf("%s (%s)", k.Name, k.ID)
}

// keyState is a key's status, or expired for a key that would otherwise work.
func keyState(k admin.Key) string {
	if k.Expired && k.Status == admin.KeyActive {
		return "expired"
	}
	return k.Status
}

func keyList(ctx context.Context, c *call) error {
	fs := c.flags("key list [--json]", true)
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	list, err := cl.ListKeys(ctx, admin.KeyQuery{})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tUPSTREAM\tSTATUS\tEXPIRES\tTAGS")
	for _, k := range list.Keys {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", k.ID, orDash(k.Name), orDash(k.Upstream), keyState(k), when(&k.ExpiresAt), strings.Join(k.Tags, ","))
	}
	return w.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// resolveKey turns a name or a key id into the key, since the API uses only
// key ids. The id is tried first, as for an upstream or a credential.
func resolveKey(ctx context.Context, cl *client, ref string) (admin.Key, error) {
	k, err := cl.GetKey(ctx, admin.KeyRef{ID: ref})
	if err == nil {
		return k, nil
	}
	var ae *admin.Error
	if !errors.As(err, &ae) || ae.Code != admin.CodeNotFound {
		return admin.Key{}, err
	}
	list, err := cl.ListKeys(ctx, admin.KeyQuery{Name: ref})
	if err != nil {
		return admin.Key{}, err
	}
	if len(list.Keys) == 0 {
		return admin.Key{}, fmt.Errorf("no key has the name or key id %q", ref)
	}
	return list.Keys[0], nil
}

func keyShow(ctx context.Context, c *call) error {
	fs := c.flags("key show <name or key id> [--json]", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveKey(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(k)
	}
	printKey(c, k)
	return nil
}

// keyPlaces is where a key has been used from, and by what: each country, AS,
// network and client, most recently seen first, with what kman forwarded from it
// and what it refused.
func keyPlaces(ctx context.Context, c *call) error {
	fs := c.flags("key places <name or key id> [--kind country|asn|network|client] [--json]", true)
	kind := fs.String("kind", "", "only places of this kind: country, asn, network or client")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveKey(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	got, err := cl.Places(ctx, admin.PlaceQuery{ID: k.ID, Kind: *kind})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	const withheld = "Networks are not shown: a network is a client's /24 or /64, which identifies a person or an office. kman settings set expose_client_addresses true shows them."
	if len(got.Places) == 0 {
		if got.NetworksWithheld {
			fmt.Fprintln(c.env.Stdout, withheld)
		} else {
			fmt.Fprintf(c.env.Stdout, "Key %s has not been used yet.\n", keyLabel(k))
		}
		return nil
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KIND\tVALUE\tLABEL\tFIRST SEEN\tLAST SEEN\tFORWARDED\tREFUSED")
	for _, p := range got.Places {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\n", p.Kind, placeValue(p), orDash(cleanDashboard(p.Label)),
			when(&p.FirstSeen), when(&p.LastSeen), p.Requests, p.Refused)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if got.NetworksWithheld {
		fmt.Fprintln(c.env.Stdout, "\n"+withheld)
	}
	return nil
}

// placeValue is how a place reads: an AS as AS3320, and the row that counts
// the places past the limit as other.
func placeValue(p admin.Place) string {
	switch {
	case p.Other:
		return "other"
	case p.Kind == "asn":
		return "AS" + p.Value
	}
	return cleanDashboard(p.Value)
}

// keyClients is key clients: the addresses a key was used from, grouped, from the
// events kman keeps. The admin API gives them out only when the install turned
// expose_client_addresses on, and says so when it has not.
func keyClients(ctx context.Context, c *call) error {
	fs := c.flags("key clients <name or key id> [--json]\n\n"+
		"Lists the client addresses a key was used from, the most recently seen first, with what each did. "+
		"It needs the expose_client_addresses setting: kman settings set expose_client_addresses true.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveKey(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	got, err := cl.KeyClients(ctx, admin.KeyRef{ID: k.ID})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	printClients(c, k, got)
	return nil
}

// printClients is the table of addresses a key was used from.
func printClients(c *call, k admin.Key, got admin.KeyClients) {
	if len(got.Clients) == 0 {
		fmt.Fprintf(c.env.Stdout, "%s has no client addresses in the events kman keeps.\n", orDash(k.Name))
		return
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tREQUESTS\tBLOCKED\tLAST SEEN\tFIRST SEEN\tCOUNTRY\tASN\tCOST")
	unmetered := false
	for _, cl := range got.Clients {
		cost := dollars(cl.CostMicro)
		if cl.UnmeteredRequests > 0 {
			cost += "+"
			unmetered = true
		}
		asn := "-"
		if cl.ASN != 0 {
			asn = fmt.Sprintf("AS%d", cl.ASN)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", cl.Address, cl.Requests, cl.Blocked,
			when(&cl.LastSeen), when(&cl.FirstSeen), orDash(cl.Country), asn, cost)
	}
	w.Flush()
	if got.Total > len(got.Clients) {
		fmt.Fprintf(c.env.Stdout, "\nThe %d most recently seen of %d addresses.\n", len(got.Clients), got.Total)
	}
	if unmetered {
		fmt.Fprintln(c.env.Stdout, "\n+ some of its requests have no known cost, which the total leaves out.")
	}
}

func printKey(c *call, k admin.Key) {
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	line := func(name, value string) {
		if value != "" {
			fmt.Fprintf(w, "%s:\t%s\n", name, value)
		}
	}
	line("id", k.ID)
	line("name", k.Name)
	if k.Kind == "canary" {
		line("kind", "canary: it forwards nothing, and kman canary manages it")
	}
	line("upstream", k.Upstream)
	line("credential", k.Credential)
	line("status", keyState(k))
	line("created", when(&k.CreatedAt))
	if k.ExpiresAt.IsZero() { // a canary has no expiry
		line("expires", "never")
	} else {
		line("expires", when(&k.ExpiresAt))
	}
	line("note", k.Note)
	line("tags", strings.Join(k.Tags, ", "))
	line("policy", fmt.Sprintf("%s (version %d)", k.Policy, k.PolicyVersion))
	if k.Spend != nil {
		line("spend today", spendLine(k.Spend.Today))
		line("spend month", spendLine(k.Spend.Month))
	}
	w.Flush()
}

// spendLine is a key's spend in a period beside its cap: what an operator watches
// until events and metrics arrive. Requests whose cost is unknown are said so,
// and never counted as $0.
func spendLine(p admin.SpendPeriod) string {
	var b strings.Builder
	b.WriteString(dollars(p.CostMicro))
	if p.CapMicro > 0 {
		fmt.Fprintf(&b, " of %s cap", dollars(p.CapMicro))
	} else {
		b.WriteString(", no cap")
	}
	if p.UnmeteredRequests > 0 {
		fmt.Fprintf(&b, " + %d unmetered %s", p.UnmeteredRequests, plural(p.UnmeteredRequests, "request", "requests"))
	}
	details := []string{fmt.Sprintf("%d %s", p.Requests, plural(p.Requests, "request", "requests"))}
	if p.EstimatedMicro > 0 {
		details = append(details, dollars(p.EstimatedMicro)+" estimated")
	}
	if p.ReservedMicro > 0 {
		details = append(details, dollars(p.ReservedMicro)+" in flight")
	}
	fmt.Fprintf(&b, " (%s: %s)", p.Period, strings.Join(details, ", "))
	return b.String()
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func keyUpdate(ctx context.Context, c *call) error {
	const usage = "key update <name or key id> [--name <name>] [--note <text>] [--tag <tag>]... [--no-tags] [--ttl <duration>] [--policy <json>] [--json]"
	fs := c.flags(usage, true)
	name := fs.String("name", "", "a new name, or empty for none")
	note := fs.String("note", "", "a new note, or empty for none")
	ttl := fs.String("ttl", "", "a new expiry, this long from now, such as 90d or 36h")
	policy := fs.String("policy", "", "the new policy as JSON, which replaces the old one")
	noTags := fs.Bool("no-tags", false, "remove every tag")
	tags := tagFlag(fs)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	if len(*tags) > 0 && *noTags {
		return errors.New("--tag contradicts --no-tags")
	}
	var req admin.KeyUpdate
	var problem error
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "name":
			req.Name = name
		case "note":
			req.Note = note
		case "tag":
			req.Tags = tags
		case "no-tags":
			if *noTags {
				req.Tags = &[]string{}
			}
		case "ttl":
			n, err := ttlFlag(*ttl)
			problem = errors.Join(problem, err)
			req.TTLSeconds = &n
		case "policy":
			raw, err := policyValue(*policy)
			problem = errors.Join(problem, err)
			req.Policy = raw
		}
	})
	if problem != nil {
		return problem
	}
	if req.Name == nil && req.Note == nil && req.Tags == nil && req.TTLSeconds == nil && req.Policy == nil {
		fs.Usage()
		return errReported
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveKey(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	req.ID = k.ID
	updated, err := cl.UpdateKey(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(updated)
	}
	fmt.Fprintf(c.env.Stdout, "Updated key %s. It expires %s.\n", keyLabel(updated), when(&updated.ExpiresAt))
	return nil
}
