package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/token"
)

func mintKey(t *testing.T, child bool) (secret, id string) {
	t.Helper()
	id = string(token.NewKeyID())
	mint := token.MintVirtualKey
	if child {
		mint = token.MintChildKey
	}
	m, err := mint(token.KeyID(id))
	if err != nil {
		t.Fatal(err)
	}
	return m.Reveal(), id
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanOutput is what kman scan --json prints.
type scanOutput struct {
	Scanned  int `json:"scanned"`
	Findings []struct {
		Source, Kind, Severity, Label, Redacted string
		KeyID                                   string `json:"key_id"`
		Line, Column                            int
	} `json:"findings"`
}

func TestScanFindsAKeyInADirectoryOfflineAndNamesTheKeyToRevoke(t *testing.T) {
	dir := t.TempDir()
	secret, id := mintKey(t, false)
	write(t, filepath.Join(dir, "app", ".env"), "# keys\nOPENAI_API_KEY="+secret+"\n")
	write(t, filepath.Join(dir, "README.md"), "no keys here\n")

	// Offline: no config directory, no credentials, no server.
	s, stdout, stderr := testSys(t, map[string]string{"HOME": t.TempDir()})
	if code := run(t.Context(), []string{"scan", dir}, s); code != 3 {
		t.Fatalf("kman scan exited %d, want 3 (findings exist); stderr: %s", code, stderr)
	}
	out := stdout.String()
	for _, want := range []string{filepath.Join(dir, "app", ".env") + ":2:16", "virtual key", "kmn_" + id + "_…", "kman key show " + id} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out+stderr.String(), secret[len("kmn_")+9:][:12]) {
		t.Errorf("scan printed the secret:\n%s", out)
	}
}

func TestScanReportsAChildKeyAtLowerSeverity(t *testing.T) {
	dir := t.TempDir()
	secret, _ := mintKey(t, true)
	write(t, filepath.Join(dir, "page.js"), `const k = "`+secret+`"`)
	s, stdout, _ := testSys(t, nil)
	run(t.Context(), []string{"scan", dir}, s)
	if !strings.Contains(stdout.String(), "child key (short-lived)") {
		t.Errorf("output does not label the child key:\n%s", stdout)
	}
}

func TestScanWithNothingFoundExitsZero(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "kmn_notakey\n")
	s, stdout, _ := testSys(t, nil)
	if code := run(t.Context(), []string{"scan", dir}, s); code != 0 {
		t.Errorf("kman scan exited %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "No kman keys found") {
		t.Errorf("output = %q", stdout)
	}
}

func TestScanJSONListsTheFindingsAndNeverTheSecret(t *testing.T) {
	dir := t.TempDir()
	secret, id := mintKey(t, false)
	write(t, filepath.Join(dir, "x.log"), "a\n  "+secret+"\n")
	write(t, filepath.Join(dir, "y.log"), "b\n")
	s, stdout, _ := testSys(t, nil)
	if code := run(t.Context(), []string{"scan", "--json", dir}, s); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	var got scanOutput
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.Scanned != 2 || len(got.Findings) != 1 {
		t.Fatalf("scanned %d, findings %d: %s", got.Scanned, len(got.Findings), stdout)
	}
	f := got.Findings[0]
	if f.KeyID != id || f.Line != 2 || f.Column != 3 || f.Severity != "high" || f.Source != filepath.Join(dir, "x.log") {
		t.Errorf("finding = %+v", f)
	}
	if strings.Contains(stdout.String(), secret[len("kmn_")+9:][:12]) {
		t.Errorf("JSON holds the secret: %s", stdout)
	}

	s, stdout, _ = testSys(t, nil)
	run(t.Context(), []string{"scan", "--json", filepath.Join(dir, "y.log")}, s)
	if !strings.Contains(stdout.String(), `"findings": []`) {
		t.Errorf("a clean scan's findings are not an empty list: %s", stdout)
	}
}

func TestScanReadsStandardInputAndAFileGivenDirectly(t *testing.T) {
	secret, id := mintKey(t, false)
	s, stdout, _ := testSys(t, nil)
	s.stdin = strings.NewReader("export K=" + secret)
	if code := run(t.Context(), []string{"scan", "-"}, s); code != 3 || !strings.Contains(stdout.String(), "-:1:10") || !strings.Contains(stdout.String(), id) {
		t.Errorf("stdin: exit %d, output %s", code, stdout)
	}
	file := filepath.Join(t.TempDir(), "f")
	write(t, file, secret)
	s, _, _ = testSys(t, nil)
	if code := run(t.Context(), []string{"scan", file}, s); code != 3 {
		t.Errorf("a file: exit %d, want 3", code)
	}
}

func TestScanScansEveryRegularFileAndFollowsNoSymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret, _ := mintKey(t, false)
	write(t, filepath.Join(outside, "elsewhere"), secret)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	write(t, filepath.Join(dir, ".git", "config"), "token = "+secret)
	write(t, filepath.Join(dir, "blob.bin"), "\x00\x01\x02"+secret+"\x00")
	s, stdout, _ := testSys(t, nil)
	run(t.Context(), []string{"scan", "--json", dir}, s)
	var got scanOutput
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, f := range got.Findings {
		sources = append(sources, filepath.Base(f.Source))
	}
	if len(sources) != 2 || got.Scanned != 2 {
		t.Errorf("found in %v, scanned %d; want .git/config and blob.bin only (a binary file is scanned, a symlink is not followed)", sources, got.Scanned)
	}
}

func TestScanUsageAndErrors(t *testing.T) {
	s, _, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"scan"}, s); code != 1 || !strings.Contains(stderr.String(), "usage: kman scan") {
		t.Errorf("no path: exit %d, stderr %q; want a usage error", code, stderr)
	}
	s, _, stderr = testSys(t, nil)
	if code := run(t.Context(), []string{"scan", filepath.Join(t.TempDir(), "missing")}, s); code != 1 || stderr.String() == "" {
		t.Errorf("a missing path: exit %d, stderr %q; want an error", code, stderr)
	}
}

func TestScanThatCannotReadAFileSaysSoAndDoesNotExitZero(t *testing.T) {
	if os.Geteuid() == 0 || os.PathSeparator == '\\' {
		t.Skip("file permissions do not stop this user")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "secret.txt"), "x")
	if err := os.Chmod(filepath.Join(dir, "secret.txt"), 0); err != nil {
		t.Fatal(err)
	}
	s, _, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"scan", dir}, s); code != 1 || !strings.Contains(stderr.String(), "secret.txt") {
		t.Errorf("exit %d, stderr %q; want 1 naming the file: a scan that missed a file did not find nothing", code, stderr)
	}
}
