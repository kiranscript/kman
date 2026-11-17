package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/kirank55/kman/internal/admin"
)

func cmdChannel(ctx context.Context, c *call) error {
	sub, err := c.sub("<add|list|test|rm> ...", "add", "list", "test", "rm")
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return channelAdd(ctx, c)
	case "list":
		return channelList(ctx, c)
	case "test":
		return channelTest(ctx, c)
	}
	return channelRm(ctx, c)
}

const channelAddHelp = "\n\nThe address is asked for without echo, or read as one line of stdin when piped, or from the variable --url-env names. " +
	"It is never an argument, which would end up in shell history and process lists, and a Slack address is itself a secret. " +
	"kman seals it and never shows it again. A webhook also gets a signing secret, printed once."

func channelAdd(ctx context.Context, c *call) error {
	fs := c.flags("channel add <name> --kind slack|webhook [--url-env VAR] [--min-severity medium] [--lifecycle-alerts=false] [--json]"+channelAddHelp, true)
	kind := fs.String("kind", "", "slack, or webhook for a signed webhook")
	envName := fs.String("url-env", "", "read the address from this environment variable")
	minSeverity := fs.String("min-severity", "", "the lowest severity the channel takes: low, medium (the default), high or critical")
	lifecycle := fs.Bool("lifecycle-alerts", true, "also take spend and lifecycle warnings")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	if *kind == "" {
		return errors.New("--kind is slack or webhook")
	}
	addr, err := c.credentialSecret(*envName, "Address for the "+*kind+" channel "+pos[0]+": ")
	if err != nil {
		return err
	}
	req := admin.AddChannelRequest{Name: pos[0], Kind: *kind, URL: admin.Secret(addr), MinSeverity: *minSeverity, LifecycleAlerts: lifecycle}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.AddChannel(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	ch := got.Channel
	fmt.Fprintf(c.env.Stdout, "Added channel %s (%s, %s). Its address is sealed and will not be shown again.\n", ch.Name, ch.Kind, ch.Host)
	if got.SigningSecret != "" {
		fmt.Fprintf(c.env.Stdout, "\nEach request is signed with this secret, shown only this once:\n\n  %s\n\n"+
			"A receiver checks X-Kman-Signature: v1=<hex of HMAC-SHA256 of \"<X-Kman-Timestamp>.<body>\" under the secret>.\n", string(got.SigningSecret))
	}
	fmt.Fprintf(c.env.Stdout, "\nSend a test alert:  kman channel test %s\n", ch.Name)
	return nil
}

func channelList(ctx context.Context, c *call) error {
	fs := c.flags("channel list [--json]", true)
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	list, err := cl.ListChannels(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	if len(list.Channels) == 0 {
		fmt.Fprintln(c.env.Stdout, "No alert channels. Add one with: kman channel add <name> --kind slack")
		return nil
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tHOST\tMIN SEVERITY\tWARNINGS\tPENDING\tFAILED\tLAST ERROR")
	for _, ch := range list.Channels {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", ch.Name, ch.Kind, ch.Host, ch.MinSeverity, warningsLabel(ch.LifecycleAlerts), ch.Pending, ch.Failed, ch.LastError)
	}
	return w.Flush()
}

func warningsLabel(b bool) string { return map[bool]string{true: "on", false: "off"}[b] }

func channelTest(ctx context.Context, c *call) error {
	fs := c.flags("channel test <name> [--json]\n\nSends one test alert to the channel now, whatever its minimum severity.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.TestChannel(ctx, admin.ChannelRef{Ref: pos[0]})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		if err := c.printJSON(got); err != nil {
			return err
		}
	} else if got.OK {
		fmt.Fprintf(c.env.Stdout, "Test alert delivered to %s.\n", got.Channel)
	}
	if !got.OK {
		return errors.New("the test alert to " + strconv.Quote(got.Channel) + " did not arrive: " + got.Error)
	}
	return nil
}

func channelRm(ctx context.Context, c *call) error {
	fs := c.flags("channel rm <name> [--json]\n\nRemoves the channel, wipes its address, and gives up the alerts still waiting for it.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.RemoveChannel(ctx, admin.ChannelRef{Ref: pos[0]})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Removed channel %s.\n", got.Name)
	return nil
}
