package releasecheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runScript runs scripts/<name> with args in dir and returns its output.
func runScript(t *testing.T, dir, name string, args ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join(root, "scripts", name))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

const verifyDoc = "# Verify\n\n```sh\necho setup-is-not-run >> $LOG\n```\n\n" +
	"<!-- verify:begin -->\n\nCheck one:\n\n```sh\necho \"one $TAG $V\" >> $LOG\n```\n\n" +
	"Check two:\n\n```sh\necho two >> $LOG\n```\n\n<!-- verify:end -->\n\n```sh\necho after >> $LOG\n```\n"

// The post-release job runs exactly the documented commands, so the docs
// cannot drift from what is checked.
func TestVerifyReleaseRunsTheDocumentedCommands(t *testing.T) {
	dir := t.TempDir()
	doc, log := filepath.Join(dir, "verify.md"), filepath.Join(dir, "log")
	writeFile(t, doc, verifyDoc)
	t.Setenv("LOG", log)
	if out, err := runScript(t, dir, "verify-release.sh", "v1.2.3-rc.1", doc); err != nil {
		t.Fatalf("verify-release.sh: %v\n%s", err, out)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if want := "one v1.2.3-rc.1 1.2.3-rc.1\ntwo\n"; string(got) != want {
		t.Errorf("ran %q, want %q", got, want)
	}
}

func TestVerifyReleaseFailsWhenACommandFails(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "verify.md")
	writeFile(t, doc, strings.Replace(verifyDoc, "echo two >> $LOG", "false\necho two >> $LOG", 1))
	t.Setenv("LOG", filepath.Join(dir, "log"))
	if out, err := runScript(t, dir, "verify-release.sh", "v1.2.3", doc); err == nil {
		t.Fatalf("a failing check passed:\n%s", out)
	}
}

func TestVerifyReleaseFailsWithoutCommands(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "verify.md")
	writeFile(t, doc, "# Verify\n\n```sh\necho hi\n```\n")
	if out, err := runScript(t, dir, "verify-release.sh", "v1.2.3", doc); err == nil {
		t.Fatalf("a doc with no verify block passed:\n%s", out)
	}
}

// The documented commands check every channel against the exact identity.
func TestTheVerifyDocsCheckEveryChannelAgainstTheTagIdentity(t *testing.T) {
	doc := string(read(t, "docs/verify.md"))
	begin, end := strings.Index(doc, "<!-- verify:begin -->"), strings.Index(doc, "<!-- verify:end -->")
	if begin < 0 || end < begin {
		t.Fatal("docs/verify.md has no verify:begin/end block")
	}
	block := doc[begin:end]
	const identity = "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/$TAG"
	for _, want := range []string{
		"cosign verify-blob checksums.txt",
		"--bundle checksums.txt.sigstore.json",
		"--certificate-identity \"" + identity + "\"",
		"--certificate-oidc-issuer https://token.actions.githubusercontent.com",
		"sha256sum --ignore-missing -c checksums.txt",
		"gh attestation verify \"kman_${V}_linux_amd64.tar.gz\"",
		"--bundle \"kman_${V}.intoto.jsonl\"",
		"--predicate-type https://spdx.dev/Document/v2.3",
		"--cert-identity \"" + identity + "\"",
		"gh release verify \"$TAG\"",
		"cosign verify \"ghcr.io/kirank55/kman:$V\"",
		"gh attestation verify \"oci://ghcr.io/kirank55/kman:$V\"",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the verify block lacks %q", want)
		}
	}
	if strings.Contains(block, "identity-regexp") || strings.Contains(block, "identity-regex") {
		t.Error("the verify block uses an identity regexp; pin the exact tag identity")
	}
}

func TestCompareChecksumsPassesOnlyWhenEveryRebuiltFileMatches(t *testing.T) {
	dir := t.TempDir()
	published := "aaa  kman_1.0.0_linux_amd64.tar.gz\nbbb  kman_1.0.0_linux_amd64.tar.gz.spdx.json\nccc  kman_1.0.0.tar.gz\n"
	writeFile(t, filepath.Join(dir, "published"), published)

	writeFile(t, filepath.Join(dir, "same"), "aaa  kman_1.0.0_linux_amd64.tar.gz\nccc  kman_1.0.0.tar.gz\n")
	if out, err := runScript(t, dir, "compare-checksums.sh", "same", "published"); err != nil {
		t.Errorf("matching rebuild failed: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(dir, "differs"), "aaa  kman_1.0.0_linux_amd64.tar.gz\nddd  kman_1.0.0.tar.gz\n")
	out, err := runScript(t, dir, "compare-checksums.sh", "differs", "published")
	if err == nil || !strings.Contains(out, "kman_1.0.0.tar.gz") {
		t.Errorf("a differing rebuild passed or did not name the file: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(dir, "empty"), "")
	if out, err := runScript(t, dir, "compare-checksums.sh", "empty", "published"); err == nil {
		t.Errorf("an empty rebuild passed:\n%s", out)
	}
}
