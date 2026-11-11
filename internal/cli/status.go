package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// statusTimeout bounds how long the status summary waits for the server, which
// may not be running.
const statusTimeout = 3 * time.Second

// stage is where an install stands, and the command that moves it on.
type stage struct {
	where string
	next  string
	notes []string
}

// Status prints the status summary (plain kman): where the install stands, the
// next command, how old this binary is, and how old the price table is, the
// running server's or else this binary's (SPEC §12). It reports a stage rather than judges it, so it exits 0.
func Status(ctx context.Context, env Env) int {
	c := &call{env: env, name: "status", configDir: env.ConfigDir}
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	st, cl := c.stage(ctx)
	fmt.Fprintln(env.Stdout, st.where)
	fmt.Fprintf(env.Stdout, "Next: %s\n", st.next)
	for _, n := range st.notes {
		fmt.Fprintln(env.Stdout, n)
	}
	printBinaryAge(c)
	var prices admin.PriceDataStatus
	var err error = errNoServer
	if cl != nil {
		prices, err = cl.PriceDataStatus(ctx)
	}
	if err != nil {
		if env.LocalPriceData == nil {
			return ExitOK
		}
		prices = env.LocalPriceData()
	}
	printPriceData(c, prices)
	return ExitOK
}

// printBinaryAge says when this binary was built. A binary with no stamped date
// says so, rather than saying nothing.
func printBinaryAge(c *call) {
	if c.env.BinaryAge != nil {
		if built, days, ok := c.env.BinaryAge(); ok {
			unit := "days"
			if days == 1 {
				unit = "day"
			}
			fmt.Fprintf(c.env.Stdout, "This kman binary was built %s, %d %s old.\n", built.UTC().Format(time.DateOnly), days, unit)
			return
		}
	}
	fmt.Fprintln(c.env.Stdout, "This kman binary carries no build date, so its age is unknown.")
}

var errNoServer = errors.New("no server")

// stage works out where the install stands. The client is nil unless the
// server answered it.
func (c *call) stage(ctx context.Context) (stage, *client) {
	l, err := c.loginOrNone()
	if err != nil {
		return stage{where: err.Error(), next: "kman login --admin-url <url>"}, nil
	}
	if l.token == "" && c.env.Getenv(URLEnv) == "" && (c.env.Initialised == nil || !c.env.Initialised()) {
		return stage{where: fmt.Sprintf("kman is not initialised: there is no install in %s.", c.configDir), next: "kman init"}, nil
	}
	cl, err := newClient(l.url, l.token)
	if err != nil {
		return stage{where: err.Error(), next: "kman login --admin-url <url>"}, nil
	}
	if _, err := cl.Whoami(ctx); err != nil {
		var ae *admin.Error
		if errors.As(err, &ae) && ae.Code == admin.CodeUnauthorized {
			where := fmt.Sprintf("kman is running at %s, but this CLI has no admin token.", l.url)
			if l.token != "" {
				where = fmt.Sprintf("kman is running at %s, but it refuses the token from %s.", l.url, l.tokenFrom)
			}
			return stage{where: where, next: "kman login --admin-url " + l.url}, nil
		}
		return stage{where: fmt.Sprintf("kman is not running: nothing answers at %s.", l.url), next: "kman serve"}, nil
	}
	upstreams, err := cl.ListUpstreams(ctx)
	if err != nil {
		return stage{where: fmt.Sprintf("kman at %s did not answer: %v.", l.url, err), next: "kman serve"}, nil
	}
	keys, err := cl.ListKeys(ctx, admin.KeyQuery{})
	if err != nil {
		return stage{where: fmt.Sprintf("kman at %s did not answer: %v.", l.url, err), next: "kman serve"}, nil
	}
	st := runningStage(upstreams.Upstreams, keys.Keys)
	for _, u := range upstreams.Upstreams {
		if u.UpdateAvailable {
			st.notes = append(st.notes, fmt.Sprintf("A newer preset is available for upstream %s: kman upstream sync %s", u.Name, u.Name))
		}
	}
	return st, cl
}

// runningStage is the stage of an install whose server answers.
func runningStage(upstreams []admin.Upstream, keys []admin.Key) stage {
	if len(upstreams) == 0 {
		return stage{where: "kman is running, with no upstream.", next: "kman upstream add openai --preset openai"}
	}
	withCredential := ""
	for _, u := range upstreams {
		if len(u.Credentials) > 0 {
			withCredential = u.Name
			break
		}
	}
	if withCredential == "" {
		return stage{where: "kman is running, but no upstream has a credential.",
			next: "kman credential add <name> --upstream " + upstreams[0].Name}
	}
	usable := 0
	for _, k := range keys {
		if keyState(k) == admin.KeyActive {
			usable++
		}
	}
	if usable == 0 {
		return stage{where: "kman is running, with no key that works.", next: "kman key create my-app --upstream " + withCredential}
	}
	return stage{where: fmt.Sprintf("kman is ready: %s on %s.", count(usable, "working key"), count(len(upstreams), "upstream")),
		next: "kman key list"}
}

// count is n and a noun, plural if it needs to be.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
