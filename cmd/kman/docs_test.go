package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/token"
)

// The docs give operators the published key pattern to grep for in place of the
// scan command v0.1.0 doesn't have. It must be the pattern the code publishes.
func TestTheDocsQuoteThePublishedKeyPattern(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "v0.1.0-scope.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), token.ScanPattern) {
		t.Errorf("docs/v0.1.0-scope.md does not quote token.ScanPattern %s", token.ScanPattern)
	}
}

// docFiles lists every Markdown page an operator reads.
func docFiles(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	files := []string{"README.md", "SECURITY.md", "THREAT-MODEL.md", "CHANGELOG.md"}
	for i := range files {
		files[i] = filepath.Join(root, files[i])
	}
	filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") &&
			!strings.Contains(path, filepath.Join("docs", "adr")) && !strings.Contains(path, filepath.Join("docs", "agents")) {
			files = append(files, path)
		}
		return nil
	})
	return files
}

var (
	markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	headingLine  = regexp.MustCompile(`(?m)^#{1,6} +(.+?) *$`)
)

// Every relative link in the docs reaches a file, and every #anchor reaches a
// heading in it, so a page that moves or is renamed breaks the build and not the
// reader.
func TestEveryRelativeLinkInTheDocsResolves(t *testing.T) {
	for _, file := range docFiles(t) {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue // a page not written yet is reported by its own test
		}
		for _, m := range markdownLink.FindAllStringSubmatch(string(raw), -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			path, anchor, _ := strings.Cut(target, "#")
			dest := file
			if path != "" {
				dest = filepath.Join(filepath.Dir(file), path)
			}
			info, err := os.Stat(dest)
			if err != nil {
				t.Errorf("%s links to %s, which does not exist", file, target)
				continue
			}
			if anchor == "" || info.IsDir() || !strings.HasSuffix(dest, ".md") {
				continue
			}
			body, _ := os.ReadFile(dest)
			if !hasAnchor(string(body), anchor) {
				t.Errorf("%s links to %s, but %s has no heading for #%s", file, target, dest, anchor)
			}
		}
	}
}

// hasAnchor reports whether doc has a heading GitHub gives the id anchor.
func hasAnchor(doc, anchor string) bool {
	for _, m := range headingLine.FindAllStringSubmatch(doc, -1) {
		if slug(m[1]) == anchor {
			return true
		}
	}
	return false
}

// slug is how GitHub makes a heading's anchor: lower case, code and links
// reduced to their text, punctuation dropped, spaces to hyphens.
func slug(heading string) string {
	heading = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`).ReplaceAllString(heading, "$1")
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ' || r == '-':
			b.WriteRune('-')
		case r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var (
	fence     = regexp.MustCompile("(?s)```(sh|bash|powershell)\n(.*?)```")
	inlineKma = regexp.MustCompile("`kman ([a-z][a-z-]*)([^`]*)`")
	flagToken = regexp.MustCompile(`(?:^|\s)--?([A-Za-z][A-Za-z0-9-]*)`)
)

// A command the docs tell an operator to run is a command kman has, with flags
// it has. The check reads the generated CLI reference (the help each command
// prints), so a flag renamed in the code and not in the docs fails here. It
// reads every `sh` block, and the inline `kman <command> ...` spans that name a
// real command (other spans, such as `kman is ready`, are quoted output).
func TestEveryKmanCommandInTheDocsExists(t *testing.T) {
	ref := cliReference(t)
	sections := map[string]string{}
	var cur string
	for _, line := range strings.Split(ref, "\n") {
		if name, ok := strings.CutPrefix(line, "## `"); ok {
			cur = strings.TrimSuffix(name, "`")
		} else if name, ok := strings.CutPrefix(line, "### `"); ok {
			cur = strings.TrimSuffix(name, "`")
		}
		sections[cur] += line + "\n"
	}
	known := func(name string) bool { _, ok := sections["kman "+name]; return ok }

	check := func(file, line string) {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "kman ")
		for i >= 0 {
			if i > 0 && !strings.ContainsAny(line[i-1:i], " |;(`$") {
				break
			}
			command := line[i:]
			if end := strings.IndexAny(command, "|;)&"); end >= 0 { // what kman is piped into is not kman's
				command = command[:end]
			}
			words := strings.Fields(command)
			rest := strings.Join(words[1:], " ")
			if len(words) >= 2 && !strings.HasPrefix(words[1], "-") && !strings.HasPrefix(words[1], "<") {
				name := words[1]
				if !known(name) {
					t.Errorf("%s: %q runs kman %s, which is not a command", file, line, name)
					return
				}
				key := "kman " + name
				if len(words) >= 3 {
					if _, ok := sections[key+" "+words[2]]; ok {
						key += " " + words[2]
					}
				}
				help := sections[key]
				if key == "kman "+name {
					for sub := range sections { // a command with subcommands: its flags are the subcommand's
						if strings.HasPrefix(sub, key+" ") && len(words) >= 3 && sub == key+" "+words[2] {
							help = sections[sub]
						}
					}
				}
				for _, m := range flagToken.FindAllStringSubmatch(" "+rest, -1) {
					if m[1] == "h" || m[1] == "help" {
						continue
					}
					if !regexp.MustCompile(`(?m)^\s+-`+regexp.QuoteMeta(m[1])+`(?:\s|$)`).MatchString(help) && !strings.Contains(help, "[--"+m[1]) && !strings.Contains(help, "--"+m[1]) {
						t.Errorf("%s: %q uses --%s, which %s does not take", file, line, m[1], key)
					}
				}
			}
			next := strings.Index(line[i+1:], "kman ")
			if next < 0 {
				break
			}
			i += 1 + next
		}
	}

	for _, file := range docFiles(t) {
		if strings.HasSuffix(file, filepath.Join("docs", "cli.md")) || strings.Contains(file, "CHANGELOG") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		doc := string(raw)
		for _, m := range fence.FindAllStringSubmatch(doc, -1) {
			var joined string
			for _, l := range strings.Split(m[2], "\n") {
				if strings.HasSuffix(l, "\\") {
					joined += strings.TrimSuffix(l, "\\") + " "
					continue
				}
				l = joined + l
				joined = ""
				if strings.HasPrefix(strings.TrimSpace(l), "#") {
					continue
				}
				if c := strings.Index(l, " #"); c >= 0 {
					l = l[:c]
				}
				if strings.Contains(l, "kman ") {
					check(file, l)
				}
			}
		}
		for _, m := range inlineKma.FindAllStringSubmatch(doc, -1) {
			if known(m[1]) {
				check(file, "kman "+m[1]+m[2])
			}
		}
	}
}
