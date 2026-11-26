package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/kiranscript/kman/internal/token"
)

// The configs in examples/scanners teach gitleaks, Betterleaks and trufflehog
// about kman keys. They hold copies of the published pattern, so each copy is
// tried against minted keys: a virtual key and a child key each match their own
// rule, whole, and an admin token matches neither.
func TestTheScannerConfigsFindEachKindOfKeyWithItsOwnRule(t *testing.T) {
	id := token.NewKeyID()
	virtual, err := token.MintVirtualKey(id)
	if err != nil {
		t.Fatal(err)
	}
	child, err := token.MintChildKey(id)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{
		"kman-virtual-key": virtual.Reveal(),
		"kman-child-key":   child.Reveal(),
		"admin token":      token.MintAdminToken().Reveal(),
	}

	for file, rules := range map[string]map[string]scannerRule{
		"gitleaks.toml":   gitleaksRules(t),
		"trufflehog.yaml": trufflehogRules(t),
	} {
		if len(rules) != 2 {
			t.Errorf("%s has %d rules, want kman-virtual-key and kman-child-key", file, len(rules))
		}
		for name, rule := range rules {
			for kind, key := range keys {
				text := "OPENAI_API_KEY=" + key + "\n"
				// A scanner runs a rule's regex only on text holding a keyword.
				found := ""
				if strings.Contains(text, rule.keyword) {
					found = rule.regex.FindString(text)
				}
				switch {
				case kind == name && found != key:
					t.Errorf("%s rule %s found %d characters of its %d-character key", file, name, len(found), len(key))
				case kind != name && found != "":
					t.Errorf("%s rule %s matches a %s", file, name, kind)
				}
			}
		}
	}
}

// Between them the two rules match what the published pattern matches: each is
// the pattern with its optional ek_ taken out or put in.
func TestTheScannerConfigsCopyThePublishedPattern(t *testing.T) {
	want := map[string]string{
		"kman-virtual-key": strings.Replace(token.ScanPattern, `(?:ek_)?`, ``, 1),
		"kman-child-key":   strings.Replace(token.ScanPattern, `(?:ek_)?`, `ek_`, 1),
	}
	for file, rules := range map[string]map[string]scannerRule{
		"gitleaks.toml":   gitleaksRules(t),
		"trufflehog.yaml": trufflehogRules(t),
	} {
		for name, pattern := range want {
			rule, ok := rules[name]
			if !ok {
				t.Errorf("%s has no rule %s", file, name)
				continue
			}
			if got := rule.regex.String(); got != pattern {
				t.Errorf("%s rule %s regex = %s, want %s", file, name, got, pattern)
			}
		}
	}
}

type scannerRule struct {
	regex   *regexp.Regexp
	keyword string
}

func readScannerConfig(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "examples", "scanners", name))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may end the lines with CRLF.
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// gitleaksRules reads the [[rules]] of the gitleaks config. The module has no
// TOML parser, and the file's rules are three plain lines each.
func gitleaksRules(t *testing.T) map[string]scannerRule {
	t.Helper()
	config := string(readScannerConfig(t, "gitleaks.toml"))
	if !strings.Contains(config, "[extend]\nuseDefault = true\n") {
		t.Error("gitleaks.toml does not extend the default rules, so using it would turn them all off")
	}
	field := func(block, pattern string) string {
		m := regexp.MustCompile(pattern).FindStringSubmatch(block)
		if m == nil {
			t.Fatalf("a rule in gitleaks.toml has no line matching %s:\n%s", pattern, block)
		}
		return m[1]
	}
	rules := map[string]scannerRule{}
	for _, block := range strings.Split(config, "\n[[rules]]\n")[1:] {
		rules[field(block, `(?m)^id = "([^"]+)"$`)] = scannerRule{
			regex:   regexp.MustCompile(field(block, `(?m)^regex = '''(.+)'''$`)),
			keyword: field(block, `(?m)^keywords = \["([^"]+)"\]$`),
		}
	}
	return rules
}

func trufflehogRules(t *testing.T) map[string]scannerRule {
	t.Helper()
	var config struct {
		Detectors []struct {
			Name     string            `yaml:"name"`
			Keywords []string          `yaml:"keywords"`
			Regex    map[string]string `yaml:"regex"`
			Verify   []any             `yaml:"verify"`
		} `yaml:"detectors"`
	}
	if err := yaml.Unmarshal(readScannerConfig(t, "trufflehog.yaml"), &config); err != nil {
		t.Fatal(err)
	}
	rules := map[string]scannerRule{}
	for _, d := range config.Detectors {
		if len(d.Keywords) != 1 || len(d.Regex) != 1 {
			t.Fatalf("trufflehog.yaml detector %s needs one keyword and one regex", d.Name)
		}
		// A verify endpoint would send the key somewhere.
		if len(d.Verify) != 0 {
			t.Errorf("trufflehog.yaml detector %s has a verify block; a found key is never sent anywhere", d.Name)
		}
		rules[d.Name] = scannerRule{regex: regexp.MustCompile(d.Regex["key"]), keyword: d.Keywords[0]}
	}
	return rules
}
