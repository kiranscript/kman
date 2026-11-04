package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/kirank55/kman/internal/admin"
)

// exportPage is how many entries audit export asks for at a time.
var exportPage = 1000

func cmdAudit(ctx context.Context, c *call) error {
	if len(c.args) > 0 && c.args[0] == "export" {
		c.name += " export"
		c.args = c.args[1:]
		return auditExport(ctx, c)
	}
	fs := c.flags("audit [--limit n] [--json]\n       kman audit export [--after id]", true)
	limit := fs.Int("limit", 50, "how many of the newest entries to list, up to 1000")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	page, err := cl.Audit(ctx, admin.AuditQuery{Latest: true, Limit: *limit})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(page)
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTIME\tACTOR\tACTION\tTARGET")
	for _, e := range page.Entries {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s/%s\n", e.ID, when(&e.Time), e.Actor, e.Action, e.TargetKind, e.TargetID)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if page.More {
		fmt.Fprintln(c.env.Stderr, "Older entries: kman audit --limit <n>, or kman audit export for all of them.")
	}
	return nil
}

// auditExport writes every entry after --after as JSON Lines, oldest
// first: one admin API AuditEntry per line.
func auditExport(ctx context.Context, c *call) error {
	fs := c.flags("audit export [--after id]\n\nWrites every audit entry as JSON Lines, oldest first.", true)
	after := fs.Int64("after", 0, "start after this entry id")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(c.env.Stdout)
	cursor := *after
	for {
		page, err := cl.Audit(ctx, admin.AuditQuery{After: cursor, Limit: exportPage})
		if err != nil {
			return c.explain(err, l)
		}
		for _, e := range page.Entries {
			if err := enc.Encode(e); err != nil {
				return err
			}
			cursor = e.ID
		}
		if !page.More || len(page.Entries) == 0 {
			return nil
		}
	}
}
