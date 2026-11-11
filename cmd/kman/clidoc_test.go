package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/cli"
)

var updateCLIReference = flag.Bool("update", false, "rewrite the committed CLI reference")

// cliReferencePath is the generated CLI reference (SPEC §16).
var cliReferencePath = filepath.Join("..", "..", "docs", "cli.md")

// offlineSummaries say what the commands main runs itself do. A command with no
// entry here fails TestEveryCommandIsInTheCLIReference.
var offlineSummaries = map[string]string{
	"init":    "set up an install: the config, the master key, the data directory and the owner admin token",
	"serve":   "run the proxy and the admin API",
	"keygen":  "print a new master key and store nothing",
	"rekey":   "replace the master key and the data key, with the server stopped",
	"version": "print the version, the commit and the build date",
	"geoip":   "download the DB-IP Lite GeoIP databases that country locks and events read (the one command that reaches DB-IP)",
}

// staticUsage is the usage of the commands that take no flags.
var staticUsage = map[string]string{
	"keygen":  "usage: kman keygen\n",
	"version": "usage: kman version\n",
}

// unlistedSubcommands are subcommands a command's usage does not list in
// <a|b|c> form.
var unlistedSubcommands = map[string][]string{"audit": {"export"}}

var subcommands = regexp.MustCompile(`^usage: kman \S+ <([a-z|-]+)>`)

// TestTheCLIReferenceIsCurrent fails when docs/cli.md is stale. The reference
// is the help every command prints, so it cannot say what the binary does not.
// Regenerate it with
//
//	go test ./cmd/kman -run CLIReferenceIsCurrent -update
func TestTheCLIReferenceIsCurrent(t *testing.T) {
	got := cliReference(t)
	if *updateCLIReference {
		if err := os.WriteFile(cliReferencePath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(cliReferencePath)
	if err != nil {
		t.Fatalf("%v: run go test ./cmd/kman -run CLIReferenceIsCurrent -update", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") { // a Windows checkout may have CRLF
		t.Errorf("%s is stale: run go test ./cmd/kman -run CLIReferenceIsCurrent -update", cliReferencePath)
	}
}

func TestEveryCommandIsInTheCLIReference(t *testing.T) {
	for _, name := range offline {
		if offlineSummaries[name] == "" {
			t.Errorf("the offline command %s has no summary in offlineSummaries", name)
		}
	}
	for _, name := range cli.Commands() {
		if cli.Summary(name) == "" {
			t.Errorf("the command %s has no summary", name)
		}
	}
	ref := cliReference(t)
	for _, name := range append(slices.Clone(offline), cli.Commands()...) {
		if !strings.Contains(ref, "\n## `kman "+name+"`\n") {
			t.Errorf("the CLI reference has no section for %s", name)
		}
	}
}

// cliReference writes the reference, from the help each command prints.
func cliReference(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("# CLI reference\n\n")
	b.WriteString("Generated from the help each command prints; do not edit by hand. Regenerate it with\n" +
		"`go test ./cmd/kman -run CLIReferenceIsCurrent -update`. CI fails when it is stale.\n\n")
	b.WriteString("Every command that talks to a running kman takes `--config-dir`, the directory holding the credentials file `kman init` wrote, " +
		"and every read command takes `--json`, which prints the admin API's response. `KMAN_ADMIN_URL` and `KMAN_ADMIN_TOKEN` override the credentials file. " +
		"Exit codes: 0 success, 1 an error (usage errors included), 3 findings (not used by any v0.1.0 command).\n\n")
	b.WriteString("| Command | What it does |\n|---|---|\n")
	names := append(slices.Clone(offline), cli.Commands()...)
	summary := func(name string) string {
		if s, ok := offlineSummaries[name]; ok {
			return s
		}
		return cli.Summary(name)
	}
	for _, name := range names {
		fmt.Fprintf(&b, "| [`kman %s`](#kman-%s) | %s |\n", name, name, summary(name))
	}
	for _, name := range names {
		fmt.Fprintf(&b, "\n## `kman %s`\n\n%s.\n", name, capitalise(summary(name)))
		help := helpFor(t, name)
		var subs []string
		if m := subcommands.FindStringSubmatch(help); m != nil {
			subs = strings.Split(m[1], "|")
		} else {
			fmt.Fprintf(&b, "\n```\n%s```\n", help)
		}
		subs = append(subs, unlistedSubcommands[name]...)
		for _, sub := range subs {
			fmt.Fprintf(&b, "\n### `kman %s %s`\n\n```\n%s```\n", name, sub, helpFor(t, name, sub))
		}
	}
	return b.String()
}

func capitalise(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// helpFor is what `kman <args> -h` prints, run in process, with the home
// directory spelled $HOME so the reference does not depend on whose machine
// made it.
func helpFor(t *testing.T, args ...string) string {
	t.Helper()
	if u, ok := staticUsage[strings.Join(args, " ")]; ok {
		return u
	}
	s, stdout, stderr := testSys(t, map[string]string{"HOME": "$HOME"})
	run(t.Context(), append(slices.Clone(args), "-h"), s)
	var out bytes.Buffer
	out.WriteString(stdout.String())
	out.WriteString(stderr.String())
	if out.Len() == 0 {
		t.Fatalf("kman %s -h printed nothing", strings.Join(args, " "))
	}
	// Windows joins with a backslash, which the flag package's %q doubles.
	text := strings.NewReplacer(`$HOME\\.config\\kman`, "$HOME/.config/kman", `$HOME\.config\kman`, "$HOME/.config/kman").Replace(out.String())
	// The flag package heads its own flag sets "Usage of init:".
	return strings.Replace(text, "Usage of "+args[0]+":", "usage: kman "+args[0]+" [flags]", 1)
}
