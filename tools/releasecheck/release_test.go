package releasecheck_test

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The signing identity in SPEC §15 names release.yml at the tag, so the
// workflow must run only for a pushed v* tag.
func TestReleaseRunsOnlyForAPushedVTag(t *testing.T) {
	w := loadWorkflow(t, "release.yml")
	want := map[string]any{"push": map[string]any{"tags": []any{"v*"}}}
	if !reflect.DeepEqual(w.On, want) {
		t.Errorf("release.yml triggers = %#v, want only %#v", w.On, want)
	}
}

var exactVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// The release job is the only one that writes, and it gets only the scopes
// publishing needs: the release, the OIDC token for keyless signing, the
// attestations, and the image.
func TestOnlyTheReleaseJobWrites(t *testing.T) {
	w := loadWorkflow(t, "release.yml")
	want := map[string]any{
		"contents":     "write",
		"id-token":     "write",
		"attestations": "write",
		"packages":     "write",
	}
	for id, j := range w.Jobs {
		if id == "release" {
			if !reflect.DeepEqual(j.Permissions, want) {
				t.Errorf("job release permissions = %#v, want %#v", j.Permissions, want)
			}
			continue
		}
		if perms, ok := j.Permissions.(map[string]any); ok {
			for scope, level := range perms {
				if level == "write" && scope != "id-token" && scope != "attestations" {
					t.Errorf("job %s: %s: write, only the release job may write", id, scope)
				}
			}
		}
	}
}

// GoReleaser OSS, at one exact version wherever the repo runs it, so the
// snapshot dry run and the post-release rebuild use the release's tool.
func TestGoReleaserIsOSSAtOneExactVersion(t *testing.T) {
	versions := map[string]bool{}
	for _, name := range workflowFiles(t) {
		for id, j := range loadWorkflow(t, name).Jobs {
			for _, s := range stepsUsing(j, "goreleaser/goreleaser-action") {
				if d := s.With["distribution"]; d != "goreleaser" {
					t.Errorf("%s: job %s: distribution = %q, want goreleaser (OSS)", name, id, d)
				}
				v := s.With["version"]
				if !exactVersion.MatchString(v) {
					t.Errorf("%s: job %s: GoReleaser version = %q, want an exact vX.Y.Z", name, id, v)
				}
				versions[v] = true
			}
		}
	}
	if len(versions) != 1 {
		t.Errorf("GoReleaser versions in use = %v, want exactly one", versions)
	}
	rel := loadWorkflow(t, "release.yml").Jobs["release"]
	if len(stepsUsing(rel, "goreleaser/goreleaser-action")) != 1 {
		t.Error("the release job must run GoReleaser exactly once")
	}
}

// cosign v3 writes the standard Sigstore bundle that install.sh and the
// verify docs expect.
func TestTheReleaseJobInstallsCosignV3(t *testing.T) {
	rel := loadWorkflow(t, "release.yml").Jobs["release"]
	steps := stepsUsing(rel, "sigstore/cosign-installer")
	if len(steps) != 1 {
		t.Fatalf("release job installs cosign %d times, want once", len(steps))
	}
	v := steps[0].With["cosign-release"]
	if !exactVersion.MatchString(v) || !strings.HasPrefix(v, "v3.") {
		t.Errorf("cosign-release = %q, want an exact v3.x.y", v)
	}
}

// A pull request that touches release config gets a GoReleaser snapshot run:
// the same config, unsigned and unpublished.
func TestPullRequestsTouchingReleaseConfigGetASnapshotDryRun(t *testing.T) {
	w := loadWorkflow(t, "release-snapshot.yml")
	pr, ok := w.On["pull_request"].(map[string]any)
	if !ok || len(w.On) != 1 {
		t.Fatalf("release-snapshot.yml triggers = %#v, want only pull_request", w.On)
	}
	paths := map[string]bool{}
	for _, p := range pr["paths"].([]any) {
		paths[p.(string)] = true
	}
	for _, want := range []string{
		".goreleaser.yaml", "Dockerfile", "install.sh", "go.mod", "scripts/**",
		".github/workflows/release.yml", ".github/workflows/release-snapshot.yml",
	} {
		if !paths[want] {
			t.Errorf("release-snapshot.yml paths lack %s", want)
		}
	}
	runs := 0
	for id, j := range w.Jobs {
		if j.Permissions != nil {
			t.Errorf("job %s sets permissions; the dry run needs only the read-only default", id)
		}
		for _, s := range stepsUsing(j, "goreleaser/goreleaser-action") {
			runs++
			args := strings.Fields(s.With["args"])
			for _, want := range []string{"release", "--snapshot", "--clean"} {
				if !slices.Contains(args, want) {
					t.Errorf("snapshot args %v lack %s", args, want)
				}
			}
			if !slices.ContainsFunc(args, func(a string) bool {
				return strings.HasPrefix(a, "--skip=") && slices.Contains(strings.Split(strings.TrimPrefix(a, "--skip="), ","), "sign")
			}) {
				t.Errorf("snapshot args %v must skip signing: a PR has no signing identity", args)
			}
		}
	}
	if runs != 1 {
		t.Errorf("release-snapshot.yml runs GoReleaser %d times, want once", runs)
	}
}

// After publishing, release.yml checks its own release: the documented
// verify commands, a rebuild compared by checksum, and install.sh with each
// verifier, all read-only.
func TestThePostReleaseJobsCheckThePublishedRelease(t *testing.T) {
	w := loadWorkflow(t, "release.yml")
	runs := func(j job, want string) bool {
		return slices.ContainsFunc(j.Steps, func(s step) bool { return strings.Contains(s.Run, want) })
	}
	checks := map[string]string{
		"verify":  "sh scripts/verify-release.sh",
		"rebuild": "sh scripts/compare-checksums.sh",
		"install": "sh install.sh",
	}
	for id, want := range checks {
		j, ok := w.Jobs[id]
		if !ok {
			t.Errorf("release.yml has no %s job", id)
			continue
		}
		if needs, _ := j.Needs.(string); needs != "release" {
			t.Errorf("job %s needs %v, want release", id, j.Needs)
		}
		if !runs(j, want) {
			t.Errorf("job %s does not run %q", id, want)
		}
	}
	rebuild := w.Jobs["rebuild"]
	for _, s := range stepsUsing(rebuild, "goreleaser/goreleaser-action") {
		if !strings.Contains(s.With["args"], "--skip=publish,sign,sbom,docker,announce,validate") {
			t.Errorf("rebuild args %q, want the documented rebuild", s.With["args"])
		}
	}
	install := w.Jobs["install"]
	if !strings.Contains(fmt.Sprint(install.Strategy), "macos") {
		t.Errorf("job install strategy = %v, want Linux and macOS", install.Strategy)
	}
	if !runs(install, "KMAN_REQUIRE_SIGNATURE=1") {
		t.Error("job install must run install.sh with KMAN_REQUIRE_SIGNATURE=1")
	}
	gh, cosign := -1, -1
	for i, s := range install.Steps {
		if actionPath(s.Uses) == "sigstore/cosign-installer" {
			cosign = i
		}
		if strings.Contains(s.Run, "sh install.sh") && gh < 0 && cosign < 0 {
			gh = i
		}
	}
	if gh < 0 || cosign < 0 || !slices.ContainsFunc(install.Steps[cosign:], func(s step) bool { return strings.Contains(s.Run, "sh install.sh") }) {
		t.Error("job install must run install.sh once with gh only, then again after installing cosign")
	}
}
