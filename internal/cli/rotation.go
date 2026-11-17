package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kirank55/kman/internal/admin"
)

// graceFlag parses --grace into seconds. Unlike a TTL, 0 is a grace: it is the
// default, and ends the old secret at once. The server has the last word on
// the upper limit.
func graceFlag(v string) (int64, error) {
	if v == "0" {
		return 0, nil
	}
	n, err := parseSeconds(v)
	if err != nil {
		return 0, fmt.Errorf("--grace: want a duration such as 24h or 30m, or 0 for none")
	}
	return n, nil
}

// keyRotate is key rotate: a new secret for one key, by name or key id.
func keyRotate(ctx context.Context, c *call) error {
	fs := c.flags("key rotate <name or key id> [--grace <duration>] [--json]\n\n"+
		"Replaces the key's secret and keeps the key: its id, spend, policy and history stay. The new secret is printed once. "+
		"With --grace the old secret keeps working that long, at most 30 days, so an app can be moved over without a gap; "+
		"with none, the old secret stops at the next request. A second rotation inside a grace ends the older secret at once.", true)
	grace := fs.String("grace", "0", "how long the old secret keeps working, such as 24h; 0 ends it at once")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	seconds, err := graceFlag(*grace)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	k, err := resolveKey(ctx, cl, pos[0])
	if err != nil {
		return c.explain(err, l)
	}
	got, err := cl.RotateKey(ctx, admin.RotateKeyRequest{ID: k.ID, GraceSeconds: seconds})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Rotated key %s. Its new secret, shown only this once:\n\n  %s\n\n", keyLabel(got.Key), string(got.Secret))
	if got.SDKEnv != "" {
		fmt.Fprintf(c.env.Stdout, "  export %s=%s\n\n", got.SDKEnv, string(got.Secret))
	}
	if got.PreviousValidUntil != nil {
		fmt.Fprintf(c.env.Stdout, "The old secret keeps working until %s. Move the app to the new one before then.\n", when(got.PreviousValidUntil))
	} else {
		fmt.Fprintln(c.env.Stdout, "The old secret stopped working.")
	}
	return nil
}

// credentialCompromise is credential compromise: the answer to a leaked real
// credential.
func credentialCompromise(ctx context.Context, c *call) error {
	fs := c.flags("credential compromise <name> [--credential-env VAR] [--json]\n\n"+
		"For a real credential that leaked. First revoke it at the provider and make a new one there: that is what stops the damage. "+
		"Then this replaces the credential's secret and rotates every virtual key on it at once, with no grace, and lists the apps that need new keys. "+
		"The credential keeps its id; the keys keep theirs, with their spend, policy and history."+credentialHelp, true)
	envName := credentialEnvFlag(fs)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	secret, err := c.credentialSecret(*envName, "New secret for "+pos[0]+": ")
	if err != nil {
		return err
	}
	if secret == "" {
		return errors.New("no secret given: type it, pipe it, or use --credential-env")
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.CompromiseCredential(ctx, admin.CompromiseCredentialRequest{Ref: pos[0], Secret: admin.Secret(secret)})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	w := c.env.Stdout
	fmt.Fprintf(w, "Replaced the secret of credential %s on upstream %s.\n", got.Credential.Name, got.Credential.Upstream)
	if len(got.RotatedKeys) == 0 {
		fmt.Fprintln(w, "No virtual key used it, so none was rotated.")
		return nil
	}
	fmt.Fprintf(w, "Rotated %d %s; the old secrets stopped working. These apps need new keys, shown only this once:\n",
		len(got.RotatedKeys), plural(int64(len(got.RotatedKeys)), "virtual key", "virtual keys"))
	for _, r := range got.RotatedKeys {
		fmt.Fprintf(w, "\n  %s", keyLabel(r.Key))
		if r.Key.Note != "" {
			fmt.Fprintf(w, "  %s", r.Key.Note)
		}
		if len(r.Key.Tags) > 0 {
			fmt.Fprintf(w, "  [%s]", strings.Join(r.Key.Tags, ", "))
		}
		if r.Key.Status != admin.KeyActive {
			fmt.Fprintf(w, "  (%s)", r.Key.Status)
		}
		fmt.Fprintf(w, "\n    %s\n", string(r.Secret))
	}
	return nil
}
