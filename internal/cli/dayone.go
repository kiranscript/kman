package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// backupFile is backup: a consistent copy of the database, from the running
// server, into a file this machine owns.
func backupFile(ctx context.Context, c *call) error {
	fs := c.flags("backup <file>\n\n"+
		"Writes a consistent copy of the database while kman runs. The copy holds ciphertext only: it opens only with the master key, "+
		"which is not in it, so back the master key up separately and keep it apart from the copy. "+
		"The file is made here, owner-only, and must not exist. Restore it with kman restore, with the server stopped.", false)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	path := pos[0]
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s exists: backup never overwrites a file", path)
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	d, err := cl.Backup(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	defer d.Body.Close()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, d.Body)
	if err == nil && d.Size >= 0 && n != d.Size {
		err = fmt.Errorf("the server sent %d of %d bytes", n, d.Size)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(c.env.Stdout, "Backed up the database to %s (%d bytes).\n"+
		"It holds ciphertext only and opens only with the master key, which is not in it: back the master key up separately, and keep it apart from this file.\n"+
		"To restore it, stop kman and run kman restore %s.\n", path, n, path)
	return nil
}

// pruneData is prune: delete what is older than a date.
func pruneData(ctx context.Context, c *call) error {
	fs := c.flags("prune --before <date> [--audit-log] [--json]\n\n"+
		"Deletes the events, the hourly usage and the alerts delivered before the date. Daily spend and incidents are kept forever. "+
		"With --audit-log it also deletes the audit log's entries before the date, the one thing that ever deletes from the log; "+
		"the prune itself is recorded in the log, and that record stays. Retention does the same for events, hourly usage and alerts by itself "+
		"(see retention in kman.yaml); this is for a date you choose.", true)
	before := fs.String("before", "", "delete what is older than this date: 2026-01-01 (midnight UTC) or 2026-01-01T15:04:05Z")
	auditLog := fs.Bool("audit-log", false, "also delete the audit log's entries before the date")
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	if *before == "" {
		return errors.New("--before is required: the date to prune up to, such as --before 2026-01-01")
	}
	t, err := parseDate(*before)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	res, err := cl.Prune(ctx, admin.PruneRequest{Before: t, AuditLog: *auditLog})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(res)
	}
	w := c.env.Stdout
	fmt.Fprintf(w, "Pruned everything older than %s:\n", res.Before.Format(time.RFC3339))
	fmt.Fprintf(w, "  %d %s deleted\n", res.Events, plural(res.Events, "event", "events"))
	fmt.Fprintf(w, "  %d hourly usage %s deleted\n", res.HourlyUsage, plural(res.HourlyUsage, "row", "rows"))
	fmt.Fprintf(w, "  %d delivered %s deleted\n", res.DeliveredAlerts, plural(res.DeliveredAlerts, "alert", "alerts"))
	if *auditLog {
		fmt.Fprintf(w, "  %d audit log %s deleted; the prune is recorded in the log\n", res.AuditEntries, plural(res.AuditEntries, "entry", "entries"))
	} else {
		fmt.Fprintln(w, "The audit log is untouched (kman prune --audit-log prunes it too). Daily spend and incidents are kept forever.")
	}
	return nil
}

// parseDate reads a date as midnight UTC, or a time as RFC 3339.
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--before %q is not a date: use 2026-01-01 or 2026-01-01T15:04:05Z", s)
}
