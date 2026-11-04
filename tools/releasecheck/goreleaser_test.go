package releasecheck_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type goreleaser struct {
	Version  int
	Builds   []struct {
		ID           string
		Main         string
		Env          []string
		Goos         []string
		Goarch       []string
		Ignore       []struct{ Goos, Goarch string }
		Flags        []string
		Ldflags      []string
		ModTimestamp string `yaml:"mod_timestamp"`
	}
	Archives []struct {
		Formats         []string
		FormatOverrides []struct {
			Goos    string
			Formats []string
		} `yaml:"format_overrides"`
		NameTemplate string `yaml:"name_template"`
		BuildsInfo   fileInfo `yaml:"builds_info"`
		Files        []struct {
			Src  string
			Info fileInfo
		}
	}
	Source struct {
		Enabled      bool
		NameTemplate string `yaml:"name_template"`
	}
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
		Algorithm    string
	}
	Sboms []struct {
		Artifacts string
		Documents []string
		Args      []string
	}
	Signs []struct {
		Cmd       string
		Artifacts string
		Signature string
		Args      []string
	}
	DockersV2 []struct {
		Dockerfile string
		Images     []string
		Tags       []string
		Platforms  []string
	} `yaml:"dockers_v2"`
	DockerDigest struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"docker_digest"`
	DockerSigns []struct {
		Cmd       string
		Artifacts string
		Args      []string
	} `yaml:"docker_signs"`
	Release struct {
		Draft      bool
		Prerelease string
	}
	Gomod struct{ Proxy bool }
}

type fileInfo struct{ Owner, Group, Mtime string }

// reproducible is the archive entry metadata that does not depend on the
// machine or the time of the build.
var reproducible = fileInfo{Owner: "root", Group: "root", Mtime: "{{ .CommitDate }}"}

func loadGoreleaser(t *testing.T) goreleaser {
	t.Helper()
	var g goreleaser
	decode(t, ".goreleaser.yaml", &g)
	if g.Version != 2 {
		t.Fatalf(".goreleaser.yaml version = %d, want 2", g.Version)
	}
	return g
}

// The five release targets of SPEC §3, the same five the cross-compile gate
// in ci.yml builds.
var targets = []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64"}

// buildTargets expands the build's goos × goarch minus its ignores.
func buildTargets(t *testing.T, g goreleaser) []string {
	t.Helper()
	if len(g.Builds) != 1 {
		t.Fatalf("got %d builds, want one", len(g.Builds))
	}
	b := g.Builds[0]
	var out []string
	for _, os := range b.Goos {
		for _, arch := range b.Goarch {
			if !slices.ContainsFunc(b.Ignore, func(i struct{ Goos, Goarch string }) bool {
				return i.Goos == os && i.Goarch == arch
			}) {
				out = append(out, os+"_"+arch)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestTheBuildMakesTheFiveStaticTargets(t *testing.T) {
	g := loadGoreleaser(t)
	if got := buildTargets(t, g); !slices.Equal(got, targets) {
		t.Errorf("targets = %v, want %v", got, targets)
	}
	b := g.Builds[0]
	if b.Main != "./cmd/kman" {
		t.Errorf("main = %q, want ./cmd/kman", b.Main)
	}
	if !slices.Contains(b.Env, "CGO_ENABLED=0") {
		t.Errorf("env = %v, want CGO_ENABLED=0", b.Env)
	}
}

// Reproducible: -trimpath, the commit's timestamps and never the build time,
// and VCS stamping kept (gomod.proxy would drop it).
func TestTheBuildIsReproducible(t *testing.T) {
	g := loadGoreleaser(t)
	b := g.Builds[0]
	if !slices.Contains(b.Flags, "-trimpath") {
		t.Errorf("flags = %v, want -trimpath", b.Flags)
	}
	if b.ModTimestamp != "{{ .CommitTimestamp }}" {
		t.Errorf("mod_timestamp = %q, want {{ .CommitTimestamp }}", b.ModTimestamp)
	}
	ld := strings.Join(b.Ldflags, " ")
	for _, want := range []string{"-X main.version=", "-X main.commit={{ .FullCommit }}", "-X main.date={{ .CommitDate }}"} {
		if !strings.Contains(ld, want) {
			t.Errorf("ldflags %q lack %q", ld, want)
		}
	}
	if regexp.MustCompile(`\.Date\b|\.Now\b|\.Timestamp\b`).MatchString(ld) {
		t.Errorf("ldflags %q stamp the build time", ld)
	}
	if g.Gomod.Proxy {
		t.Error("gomod.proxy drops the VCS stamp kman version prints")
	}
	for _, a := range g.Archives {
		if a.BuildsInfo != reproducible {
			t.Errorf("archive builds_info = %+v, want %+v", a.BuildsInfo, reproducible)
		}
		for _, f := range a.Files {
			if f.Info != reproducible {
				t.Errorf("archive file %s info = %+v, want %+v", f.Src, f.Info, reproducible)
			}
		}
	}
}

// archiveNames is every archive the release attaches, with the version as a
// placeholder: one per target (zip on Windows) and the source.
func archiveNames(t *testing.T, g goreleaser) []string {
	t.Helper()
	if len(g.Archives) != 1 {
		t.Fatalf("got %d archive configs, want one", len(g.Archives))
	}
	a := g.Archives[0]
	if a.NameTemplate != "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}" {
		t.Fatalf("archive name_template = %q; install.sh and the verify docs depend on it", a.NameTemplate)
	}
	if !g.Source.Enabled || g.Source.NameTemplate != "{{ .ProjectName }}_{{ .Version }}" {
		t.Fatalf("source archive = %+v, want enabled as kman_<version>", g.Source)
	}
	var out []string
	for _, tg := range buildTargets(t, g) {
		format := a.Formats
		for _, o := range a.FormatOverrides {
			if strings.HasPrefix(tg, o.Goos+"_") {
				format = o.Formats
			}
		}
		if len(format) != 1 {
			t.Fatalf("%s: formats %v, want one", tg, format)
		}
		out = append(out, fmt.Sprintf("kman_VERSION_%s.%s", tg, format[0]))
	}
	return append(out, "kman_VERSION.tar.gz")
}

func TestOneSPDX23SBOMPerArchiveAndForTheSource(t *testing.T) {
	g := loadGoreleaser(t)
	kinds := map[string]bool{}
	for _, s := range g.Sboms {
		kinds[s.Artifacts] = true
		if !slices.Equal(s.Documents, []string{"{{ .ArtifactName }}.spdx.json"}) {
			t.Errorf("sbom %s documents = %v, want <artifact>.spdx.json", s.Artifacts, s.Documents)
		}
		if !slices.Contains(s.Args, "spdx-json@2.3=$document") {
			t.Errorf("sbom %s args = %v, want SPDX 2.3 JSON", s.Artifacts, s.Args)
		}
	}
	if len(g.Sboms) != 2 || !kinds["archive"] || !kinds["source"] {
		t.Errorf("sboms cover %v, want archive and source", kinds)
	}
}

// Every archive's SBOM is attested, bound to that archive's digest.
func TestTheReleaseAttestsEverySBOM(t *testing.T) {
	g := loadGoreleaser(t)
	rel := loadWorkflow(t, "release.yml").Jobs["release"]
	attested := map[string]string{}
	for _, s := range stepsUsing(rel, "actions/attest") {
		if p := s.With["sbom-path"]; p != "" {
			attested[s.With["subject-path"]] = p
		}
	}
	const v = "${{ steps.version.outputs.version }}"
	for _, name := range archiveNames(t, g) {
		subject := "dist/" + strings.Replace(name, "VERSION", v, 1)
		if got, want := attested[subject], subject+".spdx.json"; got != want {
			t.Errorf("SBOM attestation for %s = %q, want %q", subject, got, want)
		}
		delete(attested, subject)
	}
	for s := range attested {
		t.Errorf("SBOM attestation for %s, which is not a release archive", s)
	}
}

// One keyless cosign signature, on checksums.txt, as a Sigstore bundle.
func TestCosignSignsChecksumsOnce(t *testing.T) {
	g := loadGoreleaser(t)
	if g.Checksum.NameTemplate != "checksums.txt" || g.Checksum.Algorithm != "sha256" {
		t.Errorf("checksum = %+v, want sha256 in checksums.txt", g.Checksum)
	}
	if len(g.Signs) != 1 {
		t.Fatalf("got %d signs, want one", len(g.Signs))
	}
	s := g.Signs[0]
	if s.Cmd != "cosign" || s.Artifacts != "checksum" {
		t.Errorf("sign = %s on %q, want cosign on checksum", s.Cmd, s.Artifacts)
	}
	if s.Signature != "${artifact}.sigstore.json" || !slices.Contains(s.Args, "--bundle=${signature}") {
		t.Errorf("sign writes %q with %v, want a --bundle at ${artifact}.sigstore.json", s.Signature, s.Args)
	}
	for _, a := range s.Args {
		if strings.HasPrefix(a, "--key") {
			t.Errorf("sign args %v use a key; signing is keyless", s.Args)
		}
	}
}

func TestTheImage(t *testing.T) {
	g := loadGoreleaser(t)
	if len(g.DockersV2) != 1 {
		t.Fatalf("got %d dockers_v2 images, want one", len(g.DockersV2))
	}
	d := g.DockersV2[0]
	if !slices.Equal(d.Images, []string{"ghcr.io/kirank55/kman"}) {
		t.Errorf("images = %v, want ghcr.io/kirank55/kman", d.Images)
	}
	if !slices.Equal(d.Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Errorf("platforms = %v, want linux/amd64 and linux/arm64", d.Platforms)
	}
	for _, tag := range d.Tags {
		if strings.Contains(tag, "latest") && tag != "{{ if not .Prerelease }}latest{{ end }}" {
			t.Errorf("tag %q: latest must be only on stable releases", tag)
		}
	}
	if !slices.Contains(d.Tags, "{{ if not .Prerelease }}latest{{ end }}") {
		t.Errorf("tags = %v, want latest on stable releases", d.Tags)
	}
	if g.DockerDigest.NameTemplate != "digests.txt" {
		t.Errorf("docker_digest name = %q, want digests.txt (release.yml reads it)", g.DockerDigest.NameTemplate)
	}
	if len(g.DockerSigns) != 1 || g.DockerSigns[0].Cmd != "cosign" ||
		!slices.Contains(g.DockerSigns[0].Args, "${artifact}@${digest}") {
		t.Errorf("docker_signs = %+v, want one cosign signature by digest", g.DockerSigns)
	}
	rel := loadWorkflow(t, "release.yml").Jobs["release"]
	pushed := false
	for _, s := range stepsUsing(rel, "actions/attest") {
		if s.With["push-to-registry"] == "true" && s.With["subject-name"] == "${{ env.IMAGE }}" &&
			s.With["subject-digest"] != "" {
			pushed = true
		}
	}
	if !pushed {
		t.Error("the release job must attest the image digest and push the attestation to the registry")
	}
}

var distrolessByDigest = regexp.MustCompile(`^FROM gcr\.io/distroless/static-debian12:nonroot@sha256:[0-9a-f]{64}$`)

func TestTheDockerfileCopiesTheReleasedBinaryOntoPinnedDistroless(t *testing.T) {
	var from, user []string
	for _, line := range strings.Split(string(read(t, "Dockerfile")), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "FROM "):
			from = append(from, line)
		case strings.HasPrefix(line, "USER "):
			user = append(user, line)
		case strings.HasPrefix(line, "RUN "):
			t.Errorf("Dockerfile runs %q; the image only copies the released binary", line)
		}
	}
	if len(from) != 1 || !distrolessByDigest.MatchString(from[0]) {
		t.Errorf("FROM = %v, want one distroless/static-debian12:nonroot pinned by digest", from)
	}
	if !slices.Equal(user, []string{"USER 65532:65532"}) {
		t.Errorf("USER = %v, want 65532:65532", user)
	}
}

// The draft is published only after the attestation bundle is attached.
func TestTheReleaseIsPublishedAfterItsAttestationBundle(t *testing.T) {
	g := loadGoreleaser(t)
	if !g.Release.Draft {
		t.Error("release.draft must be true: release.yml publishes it after attaching the bundle")
	}
	if g.Release.Prerelease != "auto" {
		t.Errorf("release.prerelease = %q, want auto (an -rc.N tag is a prerelease)", g.Release.Prerelease)
	}
	rel := loadWorkflow(t, "release.yml").Jobs["release"]
	last := rel.Steps[len(rel.Steps)-1]
	for _, want := range []string{"kman_${VERSION}.intoto.jsonl", "gh release upload", "--draft=false"} {
		if !strings.Contains(last.Run, want) {
			t.Errorf("the release job's last step lacks %q", want)
		}
	}
	if strings.Index(last.Run, "gh release upload") > strings.Index(last.Run, "--draft=false") {
		t.Error("the bundle must be uploaded before the release is published")
	}
	if !strings.Contains(last.Env["BUNDLES"], "steps.attest-archives.outputs.bundle-path") ||
		!strings.Contains(last.Env["BUNDLES"], "steps.attest-image.outputs.bundle-path") {
		t.Error("the intoto.jsonl must hold the archive and image attestations")
	}
}
