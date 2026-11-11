// Command kman is the kman binary. It is the composition root: the only
// package that reads the wall clock, wires the modules under internal/
// together, and may import any of them.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // spend_timezone must resolve where the OS has no zone database, such as Windows

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/cli"
	"github.com/kirank55/kman/internal/vault"
)

// Set with -ldflags -X by the release build. When they are empty, the values
// come from the VCS stamp the Go toolchain embeds, so the build date is the
// commit's date and never the time of the build.
var (
	version string
	commit  string
	date    string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := &sys{
		stdout: os.Stdout, stderr: os.Stderr,
		getenv: os.Getenv, now: time.Now, goos: runtime.GOOS,
		inContainer: func() bool { return inContainer(os.Getenv) },
		passphrase:  func(prompt string) ([]byte, error) { return terminalPassphrase(prompt, os.Stderr) },
		stdin:       os.Stdin,
		readSecret:  terminalSecret(os.Stderr),
		stopSignals: stop,
	}
	os.Exit(run(ctx, os.Args[1:], s))
}

// offline lists the commands main runs itself; every other command is one
// of the CLI's online commands. A new offline command adds its case to run
// and its name here.
var offline = []string{"init", "serve", "keygen", "rekey", "restore", "version"}

// usageLine lists the commands.
var usageLine = "usage: kman <" + strings.Join(append(slices.Clone(offline), cli.Commands()...), "|") + ">"

// run runs the command in args. ctx is cancelled by SIGINT or SIGTERM.
func run(ctx context.Context, args []string, s *sys) int {
	if len(args) == 0 {
		code := cli.Status(ctx, cliEnv(s))
		fmt.Fprintln(s.stdout, usageLine)
		return code
	}
	switch name, rest := args[0], args[1:]; name {
	case "init":
		return cmdInit(rest, s)
	case "serve":
		return cmdServe(ctx, rest, s)
	case "version":
		return cmdVersion(ctx, rest, s)
	case "keygen":
		return cmdKeygen(rest, s.stdout, s.stderr)
	case "rekey":
		return cmdRekey(ctx, rest, s)
	case "restore":
		return cmdRestore(ctx, rest, s)
	}
	if slices.Contains(cli.Commands(), args[0]) {
		return cli.Run(ctx, args, cliEnv(s))
	}
	return usage(s.stderr)
}

// cliEnv is the environment of the CLI's online commands.
func cliEnv(s *sys) cli.Env {
	configDir, _, err := defaultDirs(s.goos, s.getenv)
	if err != nil {
		configDir = "" // the CLI asks for --config-dir
	}
	return cli.Env{Stdout: s.stdout, Stderr: s.stderr, Stdin: s.stdin, Getenv: s.getenv,
		ConfigDir: configDir, ReadSecret: s.readSecret,
		LocalPriceData: func() admin.PriceDataStatus { return builtinPrices().Status(s.now()) },
		BinaryAge:      func() (time.Time, int, bool) { return binaryAge(s.now()) },
		Initialised: func() bool {
			_, err := os.Stat(filepath.Join(configDir, configFile))
			return configDir != "" && err == nil
		}}
}

// usage prints the usage line. A usage error is an error: exit code 1
// (SPEC §12).
func usage(stderr io.Writer) int {
	fmt.Fprintln(stderr, usageLine)
	return 1
}

// cmdKeygen prints a fresh master key and stores nothing. It works offline,
// so in Docker the key can be made on the host and mounted as a secret.
func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return usage(stderr)
	}
	mk := vault.NewMasterKey()
	defer clear(mk)
	text := vault.EncodeMasterKey(mk)
	defer clear(text)
	if _, err := stdout.Write(text); err != nil {
		fmt.Fprintln(stderr, "kman keygen:", err)
		return 1
	}
	return 0
}

// stamp is what a build says of itself: the release build's -X values, else the
// VCS stamp the Go toolchain embeds.
func stamp() (v, c, d string, modified bool) {
	v, c, d = version, commit, date
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v == "" {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "" {
					c = s.Value
				}
			case "vcs.time":
				if d == "" {
					d = s.Value
				}
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	return v, c, d, modified
}

// binaryAge is when this binary was built, which is its commit's date, and how
// many whole days before now that is. It is not ok for a build that carries no
// date, such as one from go install of a module path, or one with a date that
// is not RFC 3339.
func binaryAge(now time.Time) (built time.Time, ageDays int, ok bool) {
	_, _, d, _ := stamp()
	built, err := time.Parse(time.RFC3339, d)
	if err != nil {
		return time.Time{}, 0, false
	}
	return built, max(0, int(now.Sub(built).Hours()/24)), true
}

func printVersion(w io.Writer) {
	v, c, d, modified := stamp()
	if c == "" {
		c = "unknown"
	} else if modified && commit == "" {
		c += " (modified)"
	}
	if d == "" {
		d = "unknown"
	}
	fmt.Fprintf(w, "kman %s\ncommit: %s\nbuilt:  %s\n", v, c, d)
}
