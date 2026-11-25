package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// defaultEventsLimit is how many events kman events lists, and --follow starts from.
const defaultEventsLimit = 20

// cmdEvents is kman events: the recent requests through kman, or with --follow
// those that happen as the command runs. It answers "is anyone using my key
// right now?" and never shows content.
func cmdEvents(ctx context.Context, c *call) error {
	fs := c.flags("events [--key <name or key id>] [--refused] [--code <refusal code>] [--since <duration>] [--limit n] [--follow] [--json]\n\n"+
		"Lists the requests kman saw, the newest first: when, which key, what called it (CLIENT: its User-Agent reduced to a product and major version, such as openai-python/3 or curl/8, or none), from where, what operation and model, the status, and the refusal code of a request kman refused. "+
		"A request that presented no key kman could identify is listed too, as (no key), so probing shows. "+
		"It never shows a request's content, a header, a query string or a secret. "+
		"The ADDRESS is shown only with the expose_client_addresses setting on; country and AS always are. "+
		"With --follow it prints the last few events, oldest first, and then each new one as it happens, until interrupted. "+
		"Events are stored in batches every 5 seconds, so the list can trail the live feed; --json prints one event per line with --follow.", true)
	key := fs.String("key", "", "only this key's events, by name or key id")
	refused := fs.Bool("refused", false, "only requests kman refused")
	code := fs.String("code", "", "only events with this refusal code, such as invalid_key")
	since := fs.String("since", "", "only events from this long ago, such as 30m, 24h or 7d")
	limit := fs.Int("limit", defaultEventsLimit, "how many events to list, from 1 to 1000; with --follow, how many to start from")
	follow := fs.Bool("follow", false, "keep printing new events until interrupted")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	if *limit < 1 || *limit > 1000 {
		return errors.New("--limit: want a number from 1 to 1000")
	}
	req := admin.EventQuery{Code: *code, Limit: *limit}
	if *refused {
		req.Kind = admin.EventRefused
	}
	if *since != "" {
		secs, err := parseSeconds(*since)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
		if c.env.Now == nil {
			return errors.New("--since: this build cannot tell the time")
		}
		req.Since = c.env.Now().Add(-time.Duration(secs) * time.Second)
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	if *key != "" {
		k, err := resolveKey(ctx, cl, *key)
		if err != nil {
			return c.explain(err, l)
		}
		req.Key = k.ID
	}
	if *follow {
		return eventsFollow(ctx, c, cl, l, req)
	}
	page, err := cl.Events(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(page)
	}
	if len(page.Events) == 0 {
		fmt.Fprintln(c.env.Stdout, "No events match.")
		return nil
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tKEY\tCLIENT\tADDRESS\tCOUNTRY\tASN\tOPERATION\tMODEL\tSTATUS\tCODE")
	for _, e := range page.Events {
		fmt.Fprintln(w, strings.Join(eventFields(e), "\t"))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if page.More {
		fmt.Fprintln(c.env.Stdout, "\nThere are older events: kman events --limit <n> lists more.")
	}
	if page.ClientAddressesWithheld {
		fmt.Fprintln(c.env.Stdout, "\n"+addressesWithheld)
	}
	return nil
}

const addressesWithheld = "Client addresses are not shown: an address identifies a person or an office. kman settings set expose_client_addresses true shows them."

// eventFields is an event as the columns of kman events: time, key, client,
// address, country, ASN, operation, model, status and refusal code. Whatever the server
// sent is cleaned of what a terminal would act on: a model and a path come from
// the request.
func eventFields(e admin.Event) []string {
	return []string{
		e.Time.UTC().Format("2006-01-02T15:04:05Z"),
		eventKey(e),
		orDash(cleanDashboard(e.Client)),
		eventAddress(e),
		orDash(cleanDashboard(e.Country)),
		eventASN(e),
		orDash(cleanDashboard(e.Operation)),
		orDash(cleanDashboard(e.Model)),
		eventStatus(e),
		orDash(cleanDashboard(e.RefusalCode)),
	}
}

// eventKey is the key's name and id, or says that no key was identified.
func eventKey(e admin.Event) string {
	switch {
	case e.UnidentifiedKey || e.KeyID == "":
		return "(no key)"
	case e.KeyName == "":
		return cleanDashboard(e.KeyID)
	}
	return cleanDashboard(e.KeyName) + " (" + cleanDashboard(e.KeyID) + ")"
}

// eventAddress is the client address, or says it is withheld.
func eventAddress(e admin.Event) string {
	switch {
	case e.ClientAddressWithheld:
		return "(withheld)"
	case e.ClientAddress == "":
		return "-"
	}
	return cleanDashboard(e.ClientAddress)
}

// eventASN is an AS as AS3320 and its organisation, when there is one.
func eventASN(e admin.Event) string {
	if e.ASN == 0 {
		return "-"
	}
	s := "AS" + strconv.FormatInt(e.ASN, 10)
	if e.ASNLabel != "" {
		s += " " + cleanDashboard(e.ASNLabel)
	}
	return s
}

func eventStatus(e admin.Event) string {
	if e.Status == 0 {
		return "-"
	}
	return strconv.Itoa(e.Status)
}

// missedEvery is how often --follow checks whether the stream lost events, so
// it says so within that time even when no event follows.
var missedEvery = time.Second

// eventsFollow prints the stored events the query picks, oldest first, and then
// every new one as it happens. Interrupting it, which ends ctx, is how it is
// meant to end; a stream that kman ended is an error.
func eventsFollow(ctx context.Context, c *call, cl *client, l login, req admin.EventQuery) error {
	page, err := cl.Events(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	st, err := cl.StreamEvents(ctx, admin.EventStreamQuery{Key: req.Key, Kind: req.Kind, Code: req.Code})
	if err != nil {
		return c.explain(err, l)
	}
	defer st.Close()
	var told bool
	enc := json.NewEncoder(c.env.Stdout)
	print := func(e admin.Event) error {
		if e.ClientAddressWithheld && !told {
			told = true // said once, on stderr, so a pipe of the events stays clean
			fmt.Fprintln(c.env.Stderr, addressesWithheld)
		}
		if c.json {
			return enc.Encode(e)
		}
		_, err := fmt.Fprintln(c.env.Stdout, strings.Join(eventFields(e), "  "))
		return err
	}
	for _, e := range slices.Backward(page.Events) {
		if err := print(e); err != nil {
			return err
		}
	}
	// What was missed is said as soon as it is known, not with the next event:
	// a feed that goes quiet after a burst may have no next event.
	var missed int64
	tellMissed := func() {
		if n := st.Missed(); n > missed {
			missed = n
			fmt.Fprintf(c.env.Stderr, "kman events: this reader fell behind, so kman dropped events for it: missed %d so far.\n", n)
		}
	}
	tick := time.NewTicker(missedEvery)
	defer tick.Stop()
follow:
	for {
		select {
		case e, ok := <-st.Events():
			if !ok {
				break follow
			}
			if err := print(e); err != nil {
				return err
			}
			tellMissed()
		case <-tick.C:
			tellMissed()
		}
	}
	tellMissed()
	switch {
	case ctx.Err() != nil:
		return nil
	case st.Err() != nil:
		return st.Err()
	}
	return errors.New("the event stream ended: kman stopped or closed the connection")
}
