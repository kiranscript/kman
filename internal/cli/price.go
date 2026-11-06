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
	sub, err := c.sub("<list|set|rm|import> ...", "list", "set", "rm", "import")
	if err != nil {
		return err
	}
	switch sub {
	case "set":
		return priceSet(ctx, c)
	case "rm":
		return priceRm(ctx, c)
	case "import":
		return priceImport(ctx, c)
	}
	return priceList(ctx, c)
}

func priceList(ctx context.Context, c *call) error {
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
	if err := w.Flush(); err != nil {
		return err
	}
	printOverrides(c, list.Overrides)
	return nil
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
