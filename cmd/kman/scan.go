package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/kirank55/kman/internal/detect"
)

const scanUsage = "usage: kman scan [--json] <path>...\n" +
	"\n" +
	"Looks for kman keys in files, and in a directory's files and subdirectories. A path of - reads standard input,\n" +
	"so `git log -p | kman scan -` searches history. It works offline, with no server and no login.\n" +
	"\n" +
	"A match is confirmed by its checksum, and a secret is never printed: a finding is the file, the line and the key id.\n" +
	"Exit codes: 0 nothing found, 3 a key was found, 1 an error or a file it could not read."

// scanReport is what kman scan --json prints.
type scanReport struct {
	Scanned  int              `json:"scanned" doc:"How many files, and inputs, were read."`
	Findings []detect.Finding `json:"findings"`
}

// cmdScan finds kman keys in files, and exits 3 when it finds one (SPEC §12). It is
// offline: it reads files and asks no server, so it works on a machine that has
// never run kman.
func cmdScan(args []string, s *sys) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(s.stderr)
	fs.Usage = func() {
		fmt.Fprintln(s.stderr, scanUsage)
		fs.PrintDefaults()
	}
	asJSON := fs.Bool("json", false, "print the findings as JSON")
	if err := parseInterspersed(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 1
	}
	sc := scanner{sys: s, report: scanReport{Findings: []detect.Finding{}}}
	for _, p := range fs.Args() {
		sc.path(p)
	}
	if *asJSON {
		enc := json.NewEncoder(s.stdout)
		enc.SetIndent("", "  ")
		enc.Encode(sc.report)
	} else {
		sc.print()
	}
	switch {
	case len(sc.report.Findings) > 0:
		return 3
	case sc.failed:
		return 1
	}
	return 0
}

// parseInterspersed parses flags that may come before or after the paths.
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var paths []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		paths = append(paths, fs.Arg(0))
		args = fs.Args()[1:]
	}
	// Leave the paths where fs.Args finds them.
	return fs.Parse(append([]string{"--"}, paths...))
}

type scanner struct {
	*sys
	report scanReport
	failed bool
}

// path scans a file, standard input or a directory, and says on stderr what it
// could not read.
func (sc *scanner) path(p string) {
	if p == "-" {
		sc.read("-", sc.stdin)
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		sc.fail(err)
		return
	}
	if !info.IsDir() {
		sc.file(p)
		return
	}
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			sc.fail(err)
		case d.Type().IsRegular():
			sc.file(path)
		}
		return nil // anything else is a symlink or a device: not followed, not read
	})
}

func (sc *scanner) file(path string) {
	f, err := os.Open(path)
	if err != nil {
		sc.fail(err)
		return
	}
	defer f.Close()
	sc.read(path, f)
}

func (sc *scanner) read(source string, r io.Reader) {
	if r == nil {
		sc.fail(errors.New("there is no standard input"))
		return
	}
	found, err := detect.Scan(source, r)
	sc.report.Findings = append(sc.report.Findings, found...)
	if err != nil {
		sc.fail(fmt.Errorf("%s: %w", source, err))
		return
	}
	sc.report.Scanned++
}

func (sc *scanner) fail(err error) {
	sc.failed = true
	fmt.Fprintln(sc.stderr, "kman scan:", err)
}

func (sc *scanner) print() {
	var ids []string
	for _, f := range sc.report.Findings {
		fmt.Fprintf(sc.stdout, "%s:%d:%d: %s %s\n", f.Source, f.Line, f.Column, f.Label, f.Redacted)
		if !slices.Contains(ids, f.KeyID) {
			ids = append(ids, f.KeyID)
		}
	}
	files := "files"
	if sc.report.Scanned == 1 {
		files = "file"
	}
	if len(sc.report.Findings) == 0 {
		fmt.Fprintf(sc.stdout, "No kman keys found in %d %s.\n", sc.report.Scanned, files)
		return
	}
	fmt.Fprintf(sc.stdout, "\n%d kman %s found in %d %s.\n", len(sc.report.Findings), plural(len(sc.report.Findings), "key", "keys"), sc.report.Scanned, files)
	fmt.Fprintln(sc.stdout, "Each key id above names a key. For each, run kman key show <key id> to see it, and kman key revoke <key id> to end it.")
	for _, id := range ids {
		fmt.Fprintf(sc.stdout, "  kman key show %s\n", id)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
