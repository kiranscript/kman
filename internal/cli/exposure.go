package cli

import (
	"context"
	"fmt"

	"github.com/kirank55/kman/internal/admin"
)

// cmdExposure lists the keys that would be easy to abuse if they leaked, each
// with the command that fixes it. It exits 0 either way, so it can be run for
// a look; --strict exits 3 when there is anything to fix, for a cron job or CI
// (SPEC §12).
func cmdExposure(ctx context.Context, c *call) error {
	fs := c.flags("exposure [--strict] [--json]", true)
	strict := fs.Bool("strict", false, "exit 3 when there are findings")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	report, err := cl.Exposure(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		if err := c.printJSON(report); err != nil {
			return err
		}
	} else {
		c.printExposure(report)
	}
	if *strict && len(report.Findings) > 0 {
		return errFindings
	}
	return nil
}

func (c *call) printExposure(r admin.Exposure) {
	w := c.env.Stdout
	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "No exposed keys: %d %s checked, each locked to addresses, expiring, and in use.\n", r.KeysChecked, plural(int64(r.KeysChecked), "key", "keys"))
		return
	}
	keys, last := 0, ""
	for _, f := range r.Findings {
		if f.KeyID != last {
			if last != "" {
				fmt.Fprintln(w)
			}
			label := f.KeyID
			if f.Name != "" {
				label = fmt.Sprintf("%s (%s)", f.Name, f.KeyID)
			}
			fmt.Fprintln(w, label)
			keys++
			last = f.KeyID
		}
		fmt.Fprintf(w, "  %s: %s\n", ruleTitle(f.Rule), f.Message)
		fmt.Fprintf(w, "  Fix: %s\n", f.Fix)
	}
	fmt.Fprintf(w, "\n%d %s to fix (%d %s) of %d checked.\n", keys, plural(int64(keys), "key", "keys"),
		len(r.Findings), plural(int64(len(r.Findings)), "finding", "findings"), r.KeysChecked)
}

// ruleTitle is how a rule is said in the report.
func ruleTitle(rule string) string {
	switch rule {
	case admin.ExposureOpenToEveryAddress:
		return "open to every address"
	case admin.ExposureNeverExpires:
		return "never expires"
	case admin.ExposureUnused:
		return "unused for 30 days"
	}
	return rule
}
