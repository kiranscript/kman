package releasecheck_test

import (
	"reflect"
	"regexp"
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
