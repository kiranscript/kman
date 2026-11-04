// Command kman is the kman binary. It is the composition root: the only
// package that reads the wall clock, wires the modules under internal/
// together, and may import any of them.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usageLine lists the commands. A new command adds its case to run and its
// name here.
const usageLine = "usage: kman <version|keygen>"

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	switch name, rest := args[0], args[1:]; name {
	case "version":
		return cmdVersion(rest, stdout, stderr)
	case "keygen":
		return cmdKeygen(rest, stdout, stderr)
	}
	return usage(stderr)
}

func usage(stderr io.Writer) int {
	fmt.Fprintln(stderr, usageLine)
	return 2
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return usage(stderr)
	}
	printVersion(stdout)
	return 0
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
