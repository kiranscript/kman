package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

func cmdAdminToken(ctx context.Context, c *call) error {
	sub, err := c.sub("<create|list|rotate|revoke> ...", "create", "list", "rotate", "revoke")
	if err != nil {
		return err
	}
	if sub == "list" {
		return adminTokenList(ctx, c)
	}
	fs := c.flags("admin-token "+sub+" <name> [--json]", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	name := pos[0]
	switch sub {
	case "create":
		created, err := cl.CreateAdminToken(ctx, admin.CreateAdminTokenRequest{Name: name})
		if err != nil {
			return c.explain(err, l)
		}
		if c.json {
			return c.printJSON(created)
		}
		fmt.Fprintf(c.env.Stdout, "Created admin token %s. Its secret, shown only this once:\n\n  %s\n\n"+
			"Use it as %s, or save it on another machine with kman login.\n", name, created.Secret, TokenEnv)
		return nil
	case "rotate":
		return adminTokenRotate(ctx, c, cl, l, name)
	}
	who, err := cl.Whoami(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	revoked, err := cl.RevokeAdminToken(ctx, admin.AdminTokenName{Name: name})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(revoked)
	}
	fmt.Fprintf(c.env.Stdout, "Revoked admin token %s. It is refused from its next request on.\n", name)
	if who.Actor == name {
		fmt.Fprintf(c.env.Stdout, "That was this CLI's own token (from %s): run kman login with another one.\n", l.tokenFrom)
	}
	return nil
}

// adminTokenRotate rotates a token. When it is the CLI's own token from
// the credentials file, the file gets the new secret, so the CLI stays
// logged in.
func adminTokenRotate(ctx context.Context, c *call, cl *client, l login, name string) error {
	who, err := cl.Whoami(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	rotated, err := cl.RotateAdminToken(ctx, admin.AdminTokenName{Name: name})
	if err != nil {
		return c.explain(err, l)
	}
	self := who.Actor == name
	var saveErr error
	if self && l.tokenFrom == l.path {
		saveErr = SaveCredentials(l.path, l.url, rotated.Secret)
	}
	if c.json {
		if err := c.printJSON(rotated); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(c.env.Stdout, "Rotated admin token %s; the old secret no longer works. The new one, shown only this once:\n\n  %s\n\n", name, rotated.Secret)
	}
	switch {
	case saveErr != nil:
		return fmt.Errorf("rotated this CLI's own token, but could not save it to %s (%v): run kman login with the new secret", l.path, saveErr)
	case self && l.tokenFrom == l.path:
		fmt.Fprintf(c.env.Stderr, "Saved the new secret to %s, since it is this CLI's own token.\n", l.path)
	case self:
		fmt.Fprintf(c.env.Stderr, "That was this CLI's own token: %s still holds the old secret, so update it.\n", TokenEnv)
	}
	return nil
}

func adminTokenList(ctx context.Context, c *call) error {
	fs := c.flags("admin-token list [--json]", true)
	if _, err := c.parse(fs, 0, 0); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	list, err := cl.ListAdminTokens(ctx)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(list)
	}
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCREATED\tLAST USED\tSTATUS")
	for _, t := range list.Tokens {
		status := "live"
		if t.RevokedAt != nil {
			status = "revoked"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, when(&t.CreatedAt), when(t.LastUsedAt), status)
	}
	return w.Flush()
}

// when is a time as the CLI prints it, in UTC.
func when(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.UTC().Format("2006-01-02T15:04Z")
}
