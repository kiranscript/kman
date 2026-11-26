package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/kiranscript/kman/internal/admin"
)

func cmdCanary(ctx context.Context, c *call) error {
	sub, err := c.sub("<create|list|show|update|reset> ...", "create", "list", "show", "update", "reset")
	if err != nil {
		return err
	}
	switch sub {
	case "create":
		return canaryCreate(ctx, c)
	case "list":
		return canaryList(ctx, c)
	case "show":
		return canaryShow(ctx, c)
	case "reset":
		return canaryReset(ctx, c)
	}
	return canaryUpdate(ctx, c)
}

// listFlag is a flag that may be repeated, each use adding a value.
func listFlag(fs *flag.FlagSet, name, usage string) *[]string {
	values := &[]string{}
	fs.Func(name, usage, func(v string) error {
		*values = append(*values, v)
		return nil
	})
	return values
}

func canaryCreate(ctx context.Context, c *call) error {
	const usage = "canary create [<name>] --planted <where> [--format env|mcp|shell|ci] [--var <NAME>] [--base-url <url>] [--tag <tag>]... [--ignore <address>]... [--json]\n\n" +
		"Makes a canary: a key in the standard format that forwards nothing and raises a critical alert when anyone uses it.\n" +
		"It prints the snippet to plant, once. Plant it where a leak would find it, and say where with --planted so the alert can."
	fs := c.flags(usage, true)
	planted := fs.String("planted", "", "where it will be planted, such as \"the deploy repo's .env\" (required)")
	format := fs.String("format", "env", "the snippet to plant: env (a .env line), mcp (an MCP server config's env block), shell (an export line) or ci (a gh secret set command)")
	variable := fs.String("var", "", "the variable the snippet sets (default OPENAI_API_KEY)")
	baseURL := fs.String("base-url", "", "kman's proxy URL, so the snippet carries it and a thief who finds only the snippet can reach kman")
	tags := tagFlag(fs)
	ignore := listFlag(fs, "ignore", "an address or network (CIDR) whose use does not fire the canary, such as your own scanner; repeat it for several")
	pos, err := c.parse(fs, 0, 1)
	if err != nil {
		return err
	}
	if *planted == "" {
		fmt.Fprintln(c.env.Stderr, "kman: canary create: --planted is required: an alert names the place, so say where this canary will be planted")
		fs.Usage()
		return errReported
	}
	req := admin.CreateCanaryRequest{Planted: *planted, Format: *format, Var: *variable, BaseURL: *baseURL, Tags: *tags, Ignore: *ignore}
	if len(pos) == 1 {
		req.Name = pos[0]
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	created, err := cl.CreateCanary(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(created)
	}
	w := c.env.Stdout
	fmt.Fprintf(w, "Made canary %s, planted at %s. It is armed.\nThe snippet to plant, in the %s format, shown only this once:\n\n%s\n",
		canaryLabel(created.Canary), cleanDashboard(created.Canary.Planted), created.Format, created.Snippet)
	fmt.Fprintln(w, "Nothing in the key says it is a canary. It never expires, and its use raises a critical alert.")
	if req.BaseURL == "" {
		fmt.Fprintln(w, "A canary fires only when it is sent to kman, so plant it where kman's address is also to be found, or make another with --base-url to put the address in the snippet.")
	}
	return nil
}

// canaryLabel names a canary in a sentence.
func canaryLabel(k admin.Canary) string {
	if k.Name == "" {
		return k.ID
	}
	return fmt.Sprintf("%s (%s)", k.Name, k.ID)
}

func canaryList(ctx context.Context, c *call) error {
	fs := c.flags("canary list [--json]", true)
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	list, err := cl.ListCanaries(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	if len(list.Canaries) == 0 {
		fmt.Fprintln(c.env.Stdout, "No canaries. Make one with kman canary create --planted <where>.")
		return nil
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATE\tPLANTED\tLAST FIRED\tUSES\tLAST FROM")
	withheld := false
	for _, k := range list.Canaries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", k.ID, orDash(k.Name), k.State, cleanDashboard(k.Planted), when(k.LastFiredAt), k.Fires, originText(k.LastUse))
		withheld = withheld || (k.LastUse != nil && k.LastUse.AddressWithheld)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if withheld {
		fmt.Fprintln(c.env.Stdout, "\nAddresses are not shown: an address is personal data. kman settings set expose_client_addresses on shows them.")
	}
	return nil
}

// originText is where a canary's last use came from, in a line: the address when
// it is given out, then the country, the AS and the client, as far as they are
// known. It is "-" for a canary that has not been used.
func originText(o *admin.CanaryOrigin) string {
	if o == nil {
		return "-"
	}
	var parts []string
	if o.Address != "" {
		parts = append(parts, o.Address)
	}
	if o.Country != "" {
		parts = append(parts, o.Country)
	}
	if o.ASN != 0 {
		parts = append(parts, "AS"+strconv.FormatInt(o.ASN, 10))
	}
	if o.ASOrg != "" {
		parts = append(parts, "("+cleanDashboard(o.ASOrg)+")")
	}
	if o.Client != "" {
		parts = append(parts, cleanDashboard(o.Client))
	}
	return orDash(strings.Join(parts, " "))
}

// resolveCanary turns a name or a key id into the canary, since the API uses only
// key ids. The id is tried first, as for a key.
func resolveCanary(ctx context.Context, cl *client, ref string) (admin.Canary, error) {
	got, err := cl.GetCanary(ctx, admin.CanaryRef{ID: ref})
	if err == nil {
		return got, nil
	}
	var ae *admin.Error
	if !errors.As(err, &ae) || ae.Code != admin.CodeNotFound {
		return admin.Canary{}, err
	}
	list, err := cl.ListCanaries(ctx)
	if err != nil {
		return admin.Canary{}, err
	}
	// A revoked canary may have passed its name on, so a live one is preferred.
	var found *admin.Canary
	for i, k := range list.Canaries {
		if k.Name == ref && (found == nil || (found.State == admin.CanaryRevoked && k.State != admin.CanaryRevoked)) {
			found = &list.Canaries[i]
		}
	}
	if found == nil {
		return admin.Canary{}, fmt.Errorf("no canary has the name or key id %q", ref)
	}
	return *found, nil
}

func canaryShow(ctx context.Context, c *call) error {
	fs := c.flags("canary show <name or key id> [--json]", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveCanary(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(k)
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	line := func(name, value string) {
		if value != "" {
			fmt.Fprintf(w, "%s:\t%s\n", name, value)
		}
	}
	line("id", k.ID)
	line("name", k.Name)
	line("state", k.State)
	line("planted", cleanDashboard(k.Planted))
	line("tags", strings.Join(k.Tags, ", "))
	line("ignores", strings.Join(k.Ignore, ", "))
	line("created", when(&k.CreatedAt))
	line("armed", when(&k.ArmedAt))
	line("first fired", when(k.FiredAt))
	line("last fired", when(k.LastFiredAt))
	line("uses", strconv.FormatInt(k.Fires, 10))
	if k.LastUse != nil {
		line("last from", originText(k.LastUse))
	}
	w.Flush()
	if k.LastUse != nil && k.LastUse.AddressWithheld {
		fmt.Fprintln(c.env.Stdout, "\nThe address is not shown: kman settings set expose_client_addresses on shows it.")
	}
	return nil
}

func canaryReset(ctx context.Context, c *call) error {
	fs := c.flags("canary reset <name or key id> [--json]\n\nArms a canary again after it fired, so its next use raises an alert.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveCanary(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	got, err := cl.ResetCanary(ctx, admin.CanaryRef{ID: k.ID})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	if k.State == admin.CanaryFired {
		fmt.Fprintf(c.env.Stdout, "Canary %s is armed again: its next use raises an alert.\n", canaryLabel(got))
	} else {
		fmt.Fprintf(c.env.Stdout, "Canary %s is already armed: its next use raises an alert.\n", canaryLabel(got))
	}
	return nil
}

func canaryUpdate(ctx context.Context, c *call) error {
	const usage = "canary update <name or key id> [--planted <where>] [--tag <tag>]... [--no-tags] [--ignore <address>]... [--no-ignore] [--json]\n\n" +
		"--tag and --ignore replace the canary's tags and its ignore list."
	fs := c.flags(usage, true)
	planted := fs.String("planted", "", "a new place it was planted")
	noTags := fs.Bool("no-tags", false, "remove every tag")
	noIgnore := fs.Bool("no-ignore", false, "empty the ignore list")
	tags := tagFlag(fs)
	ignore := listFlag(fs, "ignore", "an address or network (CIDR) whose use does not fire the canary; repeat it for several")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	if len(*tags) > 0 && *noTags {
		return errors.New("--tag contradicts --no-tags")
	}
	if len(*ignore) > 0 && *noIgnore {
		return errors.New("--ignore contradicts --no-ignore")
	}
	var req admin.CanaryUpdate
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "planted":
			req.Planted = planted
		case "tag":
			req.Tags = tags
		case "no-tags":
			if *noTags {
				req.Tags = &[]string{}
			}
		case "ignore":
			req.Ignore = ignore
		case "no-ignore":
			if *noIgnore {
				req.Ignore = &[]string{}
			}
		}
	})
	if req.Planted == nil && req.Tags == nil && req.Ignore == nil {
		fs.Usage()
		return errReported
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveCanary(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	req.ID = k.ID
	updated, err := cl.UpdateCanary(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(updated)
	}
	fmt.Fprintf(c.env.Stdout, "Updated canary %s.\n", canaryLabel(updated))
	return nil
}
