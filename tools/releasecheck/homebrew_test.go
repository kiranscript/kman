package releasecheck_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The four Homebrew targets: macOS and Linux, amd64 and arm64.
var brewTargets = []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"}

// checksumsFor is a checksums.txt for version, one made-up digest per
// release file.
func checksumsFor(version string) (string, map[string]string) {
	var b strings.Builder
	sums := map[string]string{}
	for i, tg := range append(append([]string{}, targets...), "source") {
		name := fmt.Sprintf("kman_%s_%s.tar.gz", version, tg)
		switch tg {
		case "windows_amd64":
			name = fmt.Sprintf("kman_%s_%s.zip", version, tg)
		case "source":
			name = fmt.Sprintf("kman_%s.tar.gz", version)
		}
		sum := strings.Repeat(fmt.Sprintf("%x", i+1), 64)
		sums[tg] = sum
		fmt.Fprintf(&b, "%s  %s\n", sum, name)
		fmt.Fprintf(&b, "%s  %s.spdx.json\n", strings.Repeat("f", 64), name)
	}
	fmt.Fprintf(&b, "%s  kman_%s_amd64.deb\n", strings.Repeat("e", 64), version)
	return b.String(), sums
}

func formula(t *testing.T, tag, checksums string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "checksums.txt")
	writeFile(t, file, checksums)
	return runScript(t, dir, "homebrew-formula.sh", tag, file)
}

// The formula installs the release archive for the machine, pinned by the
// SHA-256 from the release's signed checksums.txt.
func TestTheFormulaPinsEachArchiveByItsChecksum(t *testing.T) {
	checksums, sums := checksumsFor("1.2.3")
	out, err := formula(t, "v1.2.3", checksums)
	if err != nil {
		t.Fatalf("homebrew-formula.sh: %v\n%s", err, out)
	}
	for _, tg := range brewTargets {
		url := fmt.Sprintf(`url "https://github.com/kirank55/kman/releases/download/v1.2.3/kman_1.2.3_%s.tar.gz"`, tg)
		pinned := regexp.MustCompile(regexp.QuoteMeta(url) + `\s+sha256 "` + sums[tg] + `"`)
		if !pinned.MatchString(out) {
			t.Errorf("the formula does not install %s with sha256 %s:\n%s", tg, sums[tg], out)
		}
	}
	for _, want := range []string{
		"class Kman < Formula", `version "1.2.3"`, `bin.install "kman"`,
		`assert_equal "kman v#{version}", shell_output("#{bin}/kman version").strip`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the formula lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "windows") || strings.Contains(out, ".deb") {
		t.Errorf("the formula names a file Homebrew cannot install:\n%s", out)
	}
	// Each platform block is the right OS and CPU: on_macos, then on_linux,
	// each with on_arm, then on_intel.
	mac, linux, ok := strings.Cut(out, "on_linux do")
	if !ok || !strings.Contains(mac, "on_macos do") {
		t.Fatalf("the formula lacks an on_macos and an on_linux block:\n%s", out)
	}
	for osName, part := range map[string]string{"darwin": mac, "linux": linux} {
		arm, intel, ok := strings.Cut(part, "on_intel do")
		if !ok || !strings.Contains(arm, "on_arm do") ||
			!strings.Contains(arm, "_"+osName+"_arm64.tar.gz") || strings.Contains(arm, "_amd64.tar.gz") ||
			!strings.Contains(intel, "_"+osName+"_amd64.tar.gz") || strings.Contains(intel, "_arm64.tar.gz") {
			t.Errorf("the %s block does not install arm64 on arm and amd64 on intel:\n%s", osName, part)
		}
	}
}

func TestTheFormulaFailsWithoutEveryArchive(t *testing.T) {
	checksums, _ := checksumsFor("1.2.3")
	var kept []string
	for _, l := range strings.Split(checksums, "\n") {
		if !strings.HasSuffix(l, "_darwin_arm64.tar.gz") {
			kept = append(kept, l)
		}
	}
	if out, err := formula(t, "v1.2.3", strings.Join(kept, "\n")); err == nil {
		t.Errorf("rendered a formula without darwin_arm64:\n%s", out)
	}
}

func TestTheFormulaNeedsASemverTag(t *testing.T) {
	checksums, _ := checksumsFor("1.2.3")
	if out, err := formula(t, "1.2.3", checksums); err == nil {
		t.Errorf("rendered a formula for a tag without the v:\n%s", out)
	}
}

func needsOf(j job) []string {
	switch n := j.Needs.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, v := range n {
			out = append(out, fmt.Sprint(v))
		}
		return out
	}
	return nil
}

const stableOnly = "${{ !contains(github.ref_name, '-') }}"

// The formula is published to kirank55/homebrew-tap for stable releases
// only, and only once the verify job has checked the release's signature:
// the formula pins the checksums that signature covers. The job pushes
// with a deploy key for the tap alone, and its token stays read-only.
func TestTheReleasePublishesTheFormulaOnceVerified(t *testing.T) {
	w := loadWorkflow(t, "release.yml")
	j, ok := w.Jobs["homebrew"]
	if !ok {
		t.Fatal("release.yml has no homebrew job")
	}
	if n := needsOf(j); !slices.Contains(n, "release") || !slices.Contains(n, "verify") {
		t.Errorf("job homebrew needs %v, want release and verify", n)
	}
	if j.If != stableOnly {
		t.Errorf("job homebrew if = %q, want %s (no formula for a release candidate)", j.If, stableOnly)
	}
	if !reflect.DeepEqual(j.Permissions, map[string]any{"contents": "read"}) {
		t.Errorf("job homebrew permissions = %#v, want contents: read", j.Permissions)
	}
	tap := false
	for _, s := range stepsUsing(j, "actions/checkout") {
		if s.With["repository"] == "kirank55/homebrew-tap" {
			tap = true
			if s.With["ssh-key"] != "${{ secrets.HOMEBREW_TAP_DEPLOY_KEY }}" {
				t.Errorf("the tap checkout ssh-key = %q, want the HOMEBREW_TAP_DEPLOY_KEY secret", s.With["ssh-key"])
			}
		}
	}
	if !tap {
		t.Error("job homebrew does not check out kirank55/homebrew-tap")
	}
	var script string
	for _, s := range j.Steps {
		script += s.Run + "\n"
	}
	for _, want := range []string{"sh scripts/homebrew-formula.sh", "Formula/kman.rb", "ruby -c", "git push"} {
		if !strings.Contains(script, want) {
			t.Errorf("job homebrew does not run %q", want)
		}
	}
}

// SPEC §15 step 6: after release, Homebrew installs the formula on macOS,
// and nothing it installed is quarantined (kman is not notarized, so a
// quarantined binary would not run).
func TestThePostReleaseJobsInstallTheFormulaUnquarantined(t *testing.T) {
	w := loadWorkflow(t, "release.yml")
	j, ok := w.Jobs["brew"]
	if !ok {
		t.Fatal("release.yml has no brew job")
	}
	if n := needsOf(j); !slices.Equal(n, []string{"homebrew"}) {
		t.Errorf("job brew needs %v, want homebrew", n)
	}
	if fmt.Sprint(j.RunsOn) != "macos-latest" {
		t.Errorf("job brew runs on %v, want macos-latest", j.RunsOn)
	}
	if !reflect.DeepEqual(j.Permissions, map[string]any{"contents": "read"}) {
		t.Errorf("job brew permissions = %#v, want contents: read", j.Permissions)
	}
	var script string
	for _, s := range j.Steps {
		script += s.Run + "\n"
	}
	for _, want := range []string{"brew install kirank55/tap/kman", "com.apple.quarantine", `grep -Fx "kman $TAG"`, "brew test kirank55/tap/kman"} {
		if !strings.Contains(script, want) {
			t.Errorf("job brew does not run %q", want)
		}
	}
}
