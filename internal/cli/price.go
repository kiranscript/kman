package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

func cmdPrice(ctx context.Context, c *call) error {
	if _, err := c.sub("list", "list"); err != nil {
		return err
	}
	fs := c.flags("price list [--preset name] [--model part-of-id] [--json]", true)
	preset := fs.String("preset", "", "only this preset's entries, or global for the global ones")
	model := fs.String("model", "", "only models whose id contains this")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	list, err := cl.ListPrices(ctx, admin.PriceQuery{Preset: *preset, Model: *model})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	printPriceData(c, list.Status)
	fmt.Fprintln(c.env.Stdout, "Prices are per million tokens.")
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SECTION\tMODEL\tINPUT\tOUTPUT\tCACHED\tMAX OUTPUT\tNOTES")
	for _, m := range list.Models {
		section := m.Preset
		if section == "" {
			section = "global"
		}
		var input, output, cached, maxOutput string
		if m.PerMinuteMicro == 0 {
			input, output = dollars(m.InputMicro), dollars(m.OutputMicro)
		}
		if m.CachedMicro != nil {
			cached = dollars(*m.CachedMicro)
		}
		if m.MaxOutput > 0 {
			maxOutput = strconv.FormatInt(m.MaxOutput, 10)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", section, m.Model, input, output, cached, maxOutput, priceNotes(m))
	}
	return w.Flush()
}

// priceNotes names what a flat price doesn't say.
func priceNotes(m admin.PriceModel) string {
	var notes []string
	if m.PerMinuteMicro > 0 {
		voice := dollars(m.PerMinuteMicro) + "/min"
		if m.PerTextInputMicro > 0 {
			voice += ", " + dollars(m.PerTextInputMicro) + "/text input"
		}
		notes = append(notes, voice)
	}
	for _, b := range m.LongContext {
		notes = append(notes, fmt.Sprintf("over %d tokens: %s/%s", b.AboveTokens, dollars(b.InputMicro), dollars(b.OutputMicro)))
	}
	if len(m.ServiceTiers) > 0 {
		notes = append(notes, "tiers: "+strings.Join(m.ServiceTiers, ", "))
	}
	for _, f := range m.ToolFees {
		notes = append(notes, f.Tool+" "+dollars(f.Micro))
	}
	if m.Reviewed {
		notes = append(notes, "reviewed")
	}
	return strings.Join(notes, "; ")
}

// dollars prints microdollars as dollars with no more digits than it takes:
// $15, $2.50, $0.025.
func dollars(micro int64) string {
	whole, frac := micro/1_000_000, micro%1_000_000
	if frac == 0 {
		return fmt.Sprintf("$%d", whole)
	}
	digits := strings.TrimRight(fmt.Sprintf("%06d", frac), "0")
	if len(digits) == 1 {
		digits += "0"
	}
	return fmt.Sprintf("$%d.%s", whole, digits)
}

func printPriceData(c *call, st admin.PriceDataStatus) {
	unit := "days"
	if st.AgeDays == 1 {
		unit = "day"
	}
	fmt.Fprintf(c.env.Stdout, "Price data generated %s, %d %s old.\n", st.GeneratedAt.UTC().Format(time.DateOnly), st.AgeDays, unit)
	if st.Notice != "" {
		fmt.Fprintln(c.env.Stdout, st.Notice)
	}
}

// statusTimeout bounds how long the status summary waits for the server, which
// may not be running.
const statusTimeout = 3 * time.Second

// Status prints the status summary (plain kman) and returns its exit code. It
// shows how old the running server's price table is, or this binary's when no
// server answers, and asks for an upgrade past 60 days. #36 adds the install's
// stages.
func Status(ctx context.Context, env Env) int {
	c := &call{env: env, name: "status", configDir: env.ConfigDir}
	st, ok := serverPriceData(ctx, c)
	if !ok {
		if env.LocalPriceData == nil {
			return ExitOK
		}
		st = env.LocalPriceData()
	}
	printPriceData(c, st)
	return ExitOK
}

func serverPriceData(ctx context.Context, c *call) (admin.PriceDataStatus, bool) {
	cl, _, err := c.connect()
	if err != nil {
		return admin.PriceDataStatus{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	st, err := cl.PriceDataStatus(ctx)
	return st, err == nil
}
