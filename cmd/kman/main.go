// Command kman is the kman binary. It is the composition root: the only
// package that reads the wall clock, wires the modules under internal/
// together, and may import any of them.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		printVersion(stdout)
		return 0
	}
	fmt.Fprintln(stderr, "usage: kman version")
	return 2
}

func printVersion(w io.Writer) {
	v, c, d, modified := version, commit, date, false
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
