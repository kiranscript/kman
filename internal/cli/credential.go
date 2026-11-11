package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/kirank55/kman/internal/admin"
)

func cmdCredential(ctx context.Context, c *call) error {
	sub, err := c.sub("<add|update|rm> ...", "add", "update", "rm")
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return credentialAdd(ctx, c)
	case "update":
		return credentialUpdate(ctx, c)
	}
	return credentialRm(ctx, c)
}

const credentialHelp = "\n\nThe secret is asked for without echo, or read as one line of stdin when piped, or from the variable --credential-env names. It is never an argument, which would end up in shell history and process lists."

// credentialEnvFlag adds --credential-env.
func credentialEnvFlag(fs *flag.FlagSet) *string {
	return fs.String("credential-env", "", "read the secret from this environment variable")
}

// credentialSecret reads a secret from the named variable, or else asks for
// it. It may return "".
func (c *call) credentialSecret(envName, prompt string) (string, error) {
	if envName == "" {
		s, err := c.readSecret(prompt)
		if err != nil {
			return "", fmt.Errorf("read the credential: %w", err)
		}
		return s, nil
	}
	s := strings.TrimSpace(c.env.Getenv(envName))
	if s == "" {
		return "", fmt.Errorf("%s is not set, or is empty", envName)
	}
	return s, nil
}

func credentialAdd(ctx context.Context, c *call) error {
	fs := c.flags("credential add <name> --upstream <upstream> [--credential-env VAR] [--json]"+credentialHelp, true)
	upstream := fs.String("upstream", "", "the upstream's name or id")
	envName := credentialEnvFlag(fs)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	if *upstream == "" {
		return errors.New("--upstream names the upstream the credential is for")
	}
	secret, err := c.credentialSecret(*envName, "Credential for "+*upstream+": ")
	if err != nil {
		return err
	}
	if secret == "" {
		return errors.New("no credential given: type it, pipe it, or use --credential-env")
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.AddCredential(ctx, admin.AddCredentialRequest{Name: pos[0], Upstream: *upstream, Secret: admin.Secret(secret)})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Added credential %s to upstream %s. It is sealed and will not be shown again.\n", got.Name, got.Upstream)
	return nil
}

func credentialUpdate(ctx context.Context, c *call) error {
	fs := c.flags("credential update <name> [--name new] [--secret] [--credential-env VAR] [--json]\n\n"+
		"With no --name, it replaces the secret. The credential keeps its id, so the virtual keys on it are not touched."+credentialHelp, true)
	name := fs.String("name", "", "a new name")
	replace := fs.Bool("secret", false, "replace the secret too, when also renaming")
	envName := credentialEnvFlag(fs)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	req := admin.CredentialUpdate{Ref: pos[0]}
	if *name != "" {
		req.Name = name
	}
	if *name == "" || *replace || *envName != "" {
		secret, err := c.credentialSecret(*envName, "New secret for "+pos[0]+": ")
		if err != nil {
			return err
		}
		if secret == "" {
			return errors.New("no secret given: type it, pipe it, or use --credential-env")
		}
		s := admin.Secret(secret)
		req.Secret = &s
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.UpdateCredential(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	if req.Secret != nil {
		fmt.Fprintf(c.env.Stdout, "Replaced the secret of credential %s. The virtual keys on it keep working with the new one.\n", got.Name)
	} else {
		fmt.Fprintf(c.env.Stdout, "Renamed credential %s to %s.\n", pos[0], got.Name)
	}
	return nil
}

func credentialRm(ctx context.Context, c *call) error {
	fs := c.flags("credential rm <name> [--json]", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.RemoveCredential(ctx, admin.CredentialRef{Ref: pos[0]})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Removed credential %s from upstream %s. Its secret is wiped.\n", got.Name, got.Upstream)
	return nil
}
