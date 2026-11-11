package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// Exit codes (SPEC §12).
const (
	ExitOK       = 0
	ExitError    = 1
	ExitFindings = 3 // a read command found something, such as kman scan
)

// Env is what the CLI reads from and writes to besides its arguments.
type Env struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Getenv         func(string) string
	// ConfigDir is the default config directory, which holds the
	// credentials file. --config-dir overrides it.
	ConfigDir string
	// ReadSecret prompts for a secret and reads it without echo. Nil reads
	// one line of Stdin, as when a token is piped in.
	ReadSecret func(prompt string) ([]byte, error)
	// LocalPriceData is how old this binary's own price table is, for when
	// the server can't be asked.
	LocalPriceData func() admin.PriceDataStatus
	// BinaryAge is when this binary was built, and how many whole days ago, for
	// the status summary. Not ok means it carries no build date.
	BinaryAge func() (built time.Time, ageDays int, ok bool)
	// Initialised reports whether kman init has set up this machine, for the
	// status summary. Nil means it has not.
	Initialised func() bool
}

// command is one online command.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, c *call) error
}

var commands = []command{
	{"login", "save an admin URL and token for the commands on this machine", cmdLogin},
	{"logout", "remove the saved admin URL and token", cmdLogout},
	{"admin-token", "create, list, rotate or revoke admin tokens", cmdAdminToken},
	{"settings", "get or set the install's settings", cmdSettings},
	{"audit", "list the audit log, or export it", cmdAudit},
	{"price", "list the price table, and set, remove or import price overrides", cmdPrice},
	{"upstream", "add, list, show, update, remove or sync upstreams", cmdUpstream},
	{"credential", "add, update or remove credentials", cmdCredential},
	{"key", "create, list, show, update, disable, enable or revoke virtual keys", cmdKey},
}

// Commands names the online commands Run serves, for kman's usage line.
func Commands() []string {
	var out []string
	for _, c := range commands {
		out = append(out, c.name)
	}
	return out
}

// Summary is the one-line description of the online command name, or "" for a
// name that is not one. The generated CLI reference uses it.
func Summary(name string) string {
	for _, c := range commands {
		if c.name == name {
			return c.summary
		}
	}
	return ""
}

// Run runs the online command args[0] and returns its exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, "kman: no command")
		return ExitError
	}
	i := slices.IndexFunc(commands, func(c command) bool { return c.name == args[0] })
	if i < 0 {
		fmt.Fprintf(env.Stderr, "kman: unknown command %q; the online commands are %s\n", args[0], strings.Join(Commands(), ", "))
		return ExitError
	}
	c := &call{env: env, name: args[0], args: args[1:]}
	err := commands[i].run(ctx, c)
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, flag.ErrHelp):
		return ExitOK
	case errors.Is(err, errFindings):
		return ExitFindings
	case errors.Is(err, errReported):
		return ExitError
	}
	fmt.Fprintf(env.Stderr, "kman %s: %v\n", c.name, err)
	return ExitError
}

var (
	// errReported is an error already written to stderr.
	errReported = errors.New("reported")
	// errFindings ends a read command that found something.
	errFindings = errors.New("findings")
)

// call is one run of a command.
type call struct {
	env  Env
	name string // such as "admin-token create"
	args []string

	configDir string
	json      bool
}

// flags is a flag set for the call, with --config-dir; withJSON adds
// --json, which every read command takes.
func (c *call) flags(usage string, withJSON bool) *flag.FlagSet {
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(c.env.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(c.env.Stderr, "usage: kman %s\n", usage)
		fs.PrintDefaults()
	}
	fs.StringVar(&c.configDir, "config-dir", c.env.ConfigDir, "the config directory holding the credentials file")
	if withJSON {
		fs.BoolVar(&c.json, "json", false, "print the admin API's JSON response")
	}
	return fs
}

// parse parses c.args with fs, flags anywhere among the arguments, and
// checks the number of positional arguments is from min to max (-1 for
// any).
func (c *call) parse(fs *flag.FlagSet, min, max int) ([]string, error) {
	var pos []string
	args := c.args
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, errReported
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	if len(pos) < min || (max >= 0 && len(pos) > max) {
		fs.Usage()
		return nil, errReported
	}
	return pos, nil
}

// sub splits off a subcommand, such as create in admin-token create.
func (c *call) sub(usage string, names ...string) (string, error) {
	if len(c.args) == 0 || !slices.Contains(names, c.args[0]) {
		fmt.Fprintf(c.env.Stderr, "usage: kman %s %s\n", c.name, usage)
		return "", errReported
	}
	name := c.args[0]
	c.name += " " + name
	c.args = c.args[1:]
	return name, nil
}
