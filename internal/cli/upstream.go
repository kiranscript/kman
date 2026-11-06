package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/kirank55/kman/internal/admin"
)

func cmdUpstream(ctx context.Context, c *call) error {
	sub, err := c.sub("<add|list|show|update|rm|sync> ...", "add", "list", "show", "update", "rm", "sync")
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return upstreamAdd(ctx, c)
	case "list":
		return upstreamList(ctx, c)
	case "show":
		return upstreamShow(ctx, c)
	case "update":
		return upstreamUpdate(ctx, c)
	case "sync":
		return upstreamSync(ctx, c)
	}
	return upstreamRm(ctx, c)
}

// pathList collects a repeated --path.
type pathList []string

func (p *pathList) String() string     { return strings.Join(*p, ", ") }
func (p *pathList) Set(v string) error { *p = append(*p, v); return nil }

// upstreamFlags are the flags that set an upstream's settings.
type upstreamFlags struct {
	fs                                                         *flag.FlagSet
	profile, host, serverName, header, username, env, basePath *string
	multiplier                                                 *string
	port                                                       *int
	tls, noTLS, inject, noInject, basic                        *bool
	paths                                                      pathList
}

func newUpstreamFlags(fs *flag.FlagSet) *upstreamFlags {
	f := &upstreamFlags{fs: fs}
	f.profile = fs.String("profile", "", "openai, anthropic or generic (the default for a custom upstream)")
	f.host = fs.String("host", "", "the host name or IP address")
	f.port = fs.Int("port", 0, "the port (default 443 with TLS, 80 without)")
	f.tls = fs.Bool("tls", false, "connect over TLS (the default)")
	f.noTLS = fs.Bool("no-tls", false, "connect over plain HTTP")
	f.serverName = fs.String("server-name", "", "the TLS server name, when it is not the host")
	fs.Var(&f.paths, "path", `allow a route, as "METHOD /path", repeated: * is any method or one segment, and a last segment of ** is the rest of the path`)
	f.inject = fs.Bool("inject-usage", false, "ask OpenAI-style streams for their usage")
	f.noInject = fs.Bool("no-inject-usage", false, "leave OpenAI-style streams as they are")
	f.header = fs.String("header", "", `present the secret in a header, as "Name: value {key}"`)
	f.basic = fs.Bool("basic", false, "present the secret as HTTP Basic auth, in the form user:password")
	f.username = fs.String("basic-username", "", "present the secret as the password of HTTP Basic auth with this username")
	f.env = fs.String("sdk-env", "", "the variable an SDK reads its key from")
	f.basePath = fs.String("sdk-base-path", "", "appended to kman's proxy URL to make an SDK's base URL")
	f.multiplier = fs.String("price-multiplier", "", "what built-in prices and install-wide overrides are multiplied by here, such as 1.1 for a regional uplift (default 1)")
	return f
}

// settings are the settings of the flags that were given.
func (f *upstreamFlags) settings() (admin.UpstreamSettings, error) {
	given := map[string]bool{}
	f.fs.Visit(func(fl *flag.Flag) { given[fl.Name] = true })
	var s admin.UpstreamSettings
	str := func(name string, v *string) *string {
		if given[name] {
			return v
		}
		return nil
	}
	s.Profile, s.Host, s.ServerName = str("profile", f.profile), str("host", f.host), str("server-name", f.serverName)
	s.SDKEnv, s.SDKBasePath = str("sdk-env", f.env), str("sdk-base-path", f.basePath)
	if given["port"] {
		s.Port = f.port
	}
	var err error
	if given["price-multiplier"] {
		ppm, err := parseMillionths(*f.multiplier)
		if err != nil {
			return s, fmt.Errorf("--price-multiplier: %v", err)
		}
		s.PriceMultiplierPPM = &ppm
	}
	if s.TLS, err = onOff(given, "tls", f.tls, "no-tls", f.noTLS); err != nil {
		return s, err
	}
	if s.InjectUsage, err = onOff(given, "inject-usage", f.inject, "no-inject-usage", f.noInject); err != nil {
		return s, err
	}
	if given["path"] {
		paths := []string(f.paths)
		s.Paths = &paths
	}
	switch {
	case given["header"] && (given["basic"] || given["basic-username"]):
		return s, errors.New("--header and --basic are two different auth styles: give one")
	case given["header"]:
		s.Auth = &admin.Auth{Style: "header", Header: *f.header}
	case given["basic"] || given["basic-username"]:
		s.Auth = &admin.Auth{Style: "basic", BasicUsername: *f.username}
	}
	return s, nil
}

// onOff is a setting with a flag for each way, nil if neither was given.
func onOff(given map[string]bool, on string, onValue *bool, off string, offValue *bool) (*bool, error) {
	if given[on] && given[off] {
		return nil, fmt.Errorf("--%s and --%s contradict each other", on, off)
	}
	v := true
	switch {
	case given[on]:
		v = *onValue
	case given[off]:
		v = !*offValue
	default:
		return nil, nil
	}
	return &v, nil
}

func upstreamAdd(ctx context.Context, c *call) error {
	fs := c.flags("upstream add [<name>] --preset <preset> [settings] [--credential-env VAR | --no-credential] [--json]\n"+
		"       kman upstream add <name> --host <host> --path \"METHOD /path\" ... [settings] [--credential-env VAR | --no-credential] [--json]\n\n"+
		"A preset fills in everything, and copies once: a newer kman never changes the upstream, kman upstream sync does."+credentialHelp, true)
	preset := fs.String("preset", "", "the preset to copy")
	envName := credentialEnvFlag(fs)
	noCredential := fs.Bool("no-credential", false, "add the upstream with no credential, to add one later")
	credentialName := fs.String("credential-name", "", "the credential's name (default the upstream's)")
	flags := newUpstreamFlags(fs)
	pos, err := c.parse(fs, 0, 1)
	if err != nil {
		return err
	}
	req := admin.AddUpstreamRequest{Preset: *preset}
	if len(pos) == 1 {
		req.Name = pos[0]
	}
	if req.Name == "" && req.Preset == "" {
		fs.Usage()
		return errReported
	}
	if req.UpstreamSettings, err = flags.settings(); err != nil {
		return err
	}
	if *noCredential && (*envName != "" || *credentialName != "") {
		return errors.New("--no-credential contradicts --credential-env and --credential-name")
	}
	if !*noCredential {
		shown := req.Name
		if shown == "" {
			shown = req.Preset
		}
		secret, err := c.credentialSecret(*envName, "Credential for "+shown+": ")
		if err != nil {
			return err
		}
		if secret == "" {
			return errors.New("no credential given: type it, pipe it, or use --credential-env; --no-credential adds the upstream without one")
		}
		req.Credential = &admin.NewCredential{Name: *credentialName, Secret: admin.Secret(secret)}
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.AddUpstream(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	from := "built by hand"
	if got.Preset != "" {
		from = fmt.Sprintf("preset %s, version %d", got.Preset, got.PresetVersion)
	}
	fmt.Fprintf(c.env.Stdout, "Added upstream %s (%s): %s.\n", got.Name, from, address(got))
	if len(got.Credentials) == 0 {
		fmt.Fprintf(c.env.Stdout, "It has no credential yet. Next: kman credential add <name> --upstream %s\n", got.Name)
		return nil
	}
	fmt.Fprintf(c.env.Stdout, "Its credential %s is sealed and will not be shown again.\nNext: kman key create <name> --upstream %s\n", got.Credentials[0].Name, got.Name)
	return nil
}

// address is where an upstream is.
func address(u admin.Upstream) string {
	scheme := "over TLS"
	if !u.TLS {
		scheme = "over plain HTTP"
	}
	return fmt.Sprintf("%s:%d %s", u.Host, u.Port, scheme)
}

// origin is where an upstream came from, as a table shows it.
func origin(u admin.Upstream) string {
	switch {
	case u.Preset == "":
		return "custom"
	case u.UpdateAvailable:
		return fmt.Sprintf("%s v%d (update available: kman upstream sync %s)", u.Preset, u.PresetVersion, u.Name)
	}
	return fmt.Sprintf("%s v%d", u.Preset, u.PresetVersion)
}

func upstreamList(ctx context.Context, c *call) error {
	fs := c.flags("upstream list [--json]", true)
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	list, err := cl.ListUpstreams(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tADDRESS\tPROFILE\tCREDENTIALS\tPRESET")
	for _, u := range list.Upstreams {
		fmt.Fprintf(w, "%s\t%s:%d\t%s\t%s\t%s\n", u.Name, u.Host, u.Port, u.Profile, credentialNames(u), origin(u))
	}
	return w.Flush()
}

func credentialNames(u admin.Upstream) string {
	if len(u.Credentials) == 0 {
		return "none"
	}
	names := make([]string, 0, len(u.Credentials))
	for _, c := range u.Credentials {
		names = append(names, c.Name)
	}
	return strings.Join(names, ",")
}

func upstreamShow(ctx context.Context, c *call) error {
	fs := c.flags("upstream show <name> [--json]", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	u, err := cl.GetUpstream(ctx, admin.UpstreamRef{Ref: pos[0]})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(u)
	}
	printUpstream(c, u)
	return nil
}

func printUpstream(c *call, u admin.Upstream) {
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	line := func(name, value string) {
		if value != "" {
			fmt.Fprintf(w, "%s:\t%s\n", name, value)
		}
	}
	line("name", u.Name)
	line("id", u.ID)
	line("preset", origin(u))
	line("profile", u.Profile)
	line("address", address(u))
	line("server name", u.ServerName)
	line("auth", authLine(u.Auth))
	line("inject usage", fmt.Sprint(u.InjectUsage))
	line("sdk env", u.SDKEnv)
	line("sdk base path", u.SDKBasePath)
	line("price multiplier", "x"+decimal(u.PriceMultiplierPPM))
	created := u.CreatedAt
	updated := u.UpdatedAt
	line("created", when(&created))
	line("updated", when(&updated))
	w.Flush()
	fmt.Fprintln(c.env.Stdout, "path allowlist:")
	for _, p := range u.Paths {
		fmt.Fprintf(c.env.Stdout, "  %s\n", p)
	}
	fmt.Fprintln(c.env.Stdout, "credentials:")
	if len(u.Credentials) == 0 {
		fmt.Fprintln(c.env.Stdout, "  none")
		return
	}
	cw := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	for _, cr := range u.Credentials {
		last4 := "****"
		if cr.Last4 != "" {
			last4 = "..." + cr.Last4
		}
		fmt.Fprintf(cw, "  %s\t%s\t%s\tcreated %s\trotated %s\n", cr.Name, cr.ID, last4, when(&cr.CreatedAt), when(cr.RotatedAt))
	}
	cw.Flush()
}

func authLine(a admin.Auth) string {
	switch {
	case a.Style == "header":
		return a.Header
	case a.BasicUsername != "":
		return "HTTP Basic, username " + a.BasicUsername
	}
	return "HTTP Basic, the secret is user:password"
}

func upstreamUpdate(ctx context.Context, c *call) error {
	fs := c.flags("upstream update <name> [--name new] [settings] [--json]\n\nOnly the flags given change. --path replaces the whole allowlist.", true)
	name := fs.String("name", "", "a new name")
	flags := newUpstreamFlags(fs)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	req := admin.UpstreamUpdate{Ref: pos[0]}
	if req.UpstreamSettings, err = flags.settings(); err != nil {
		return err
	}
	if fs.NFlag() == 0 {
		fs.Usage()
		return errReported
	}
	if *name != "" {
		req.Name = name
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.UpdateUpstream(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	printUpstream(c, got)
	return nil
}

func upstreamRm(ctx context.Context, c *call) error {
	fs := c.flags("upstream rm <name> [--json]\n\nRemoves its credentials and price overrides too. Refused while a virtual key that is not revoked depends on one.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.RemoveUpstream(ctx, admin.UpstreamRef{Ref: pos[0]})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Removed upstream %s and its credentials (%s).\n", got.Name, credentialNames(got))
	return nil
}

func upstreamSync(ctx context.Context, c *call) error {
	fs := c.flags("upstream sync <name> [--yes] [--json]\n\n"+
		"Shows how the upstream differs from the newer preset this kman holds, and applies that after a confirmation.\n"+
		"This replaces any edit made since the last copy. --json prints the plan and applies nothing, unless --yes is given too.", true)
	yes := fs.Bool("yes", false, "apply without asking")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	ref := admin.UpstreamRef{Ref: pos[0]}
	plan, err := cl.PlanUpstreamSync(ctx, ref)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json && !*yes {
		return c.printJSON(plan)
	}
	if plan.UpToDate {
		if c.json {
			got, err := cl.GetUpstream(ctx, ref)
			if err != nil {
				return c.explain(err, l)
			}
			return c.printJSON(got)
		}
		fmt.Fprintf(c.env.Stdout, "Upstream %s is up to date, at version %d of preset %s.\n", plan.Upstream, plan.FromVersion, plan.Preset)
		return nil
	}
	if !c.json {
		printPlan(c, plan)
	}
	if !*yes {
		fmt.Fprint(c.env.Stdout, "Apply this? [y/N] ")
		answer, err := c.readLine()
		if err != nil {
			return fmt.Errorf("read the answer: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
		case "":
			fmt.Fprintln(c.env.Stdout)
			return errors.New("not applied: no answer. Run it where it can ask, or pass --yes")
		default:
			fmt.Fprintln(c.env.Stdout, "Nothing changed.")
			return nil
		}
	}
	got, err := cl.SyncUpstream(ctx, admin.UpstreamSyncRequest{Ref: pos[0], ToVersion: plan.ToVersion, Generation: plan.Generation})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Synced upstream %s to version %d of preset %s.\n", got.Name, got.PresetVersion, got.Preset)
	return nil
}

// printPlan shows what a sync would change.
func printPlan(c *call, plan admin.UpstreamSyncPlan) {
	fmt.Fprintf(c.env.Stdout, "Upstream %s is at version %d of preset %s, and this kman holds version %d.\n", plan.Upstream, plan.FromVersion, plan.Preset, plan.ToVersion)
	if len(plan.Changes) == 0 {
		fmt.Fprintln(c.env.Stdout, "Nothing about the upstream changes: only the version it records.")
		return
	}
	fmt.Fprintln(c.env.Stdout, "Syncing changes:")
	for _, ch := range plan.Changes {
		switch {
		case ch.From == "":
			fmt.Fprintf(c.env.Stdout, "  %s: + %s\n", ch.Field, ch.To)
		case ch.To == "":
			fmt.Fprintf(c.env.Stdout, "  %s: - %s\n", ch.Field, ch.From)
		default:
			fmt.Fprintf(c.env.Stdout, "  %s: %s -> %s\n", ch.Field, ch.From, ch.To)
		}
	}
}
