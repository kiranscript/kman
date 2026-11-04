package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The signing identity of SPEC §15, which install.sh must pin exactly.
const (
	identityPrefix = "https://github.com/kirank55/kman/.github/workflows/release.yml@refs/tags/"
	issuer         = "https://token.actions.githubusercontent.com"
)

// release is a fake GitHub release that install.sh downloads from.
type release struct {
	tag       string
	archive   string // file name
	files     map[string][]byte
	latestTag string // what /releases/latest redirects to
}

// newRelease packs the kman binary under test into a release for this
// platform, with a checksums.txt that lists it.
func newRelease(t *testing.T, tag string) *release {
	t.Helper()
	version := strings.TrimPrefix(tag, "v")
	archive := fmt.Sprintf("kman_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	data := tarball(t, kman)
	sum := sha256.Sum256(data)
	return &release{
		tag:     tag,
		archive: archive,
		files: map[string][]byte{
			archive:                             data,
			"checksums.txt":                     []byte(hex.EncodeToString(sum[:]) + "  " + archive + "\n"),
			"checksums.txt.sigstore.json":       []byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json"}`),
			"kman_" + version + ".intoto.jsonl": []byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json"}` + "\n"),
		},
		latestTag: tag,
	}
}

func tarball(t *testing.T, binary string) []byte {
	t.Helper()
	bin, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "kman", Mode: 0o755, Size: int64(len(bin))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(bin); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serve starts a server shaped like github.com/kirank55/kman/releases.
func (r *release) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/releases/latest" {
			http.Redirect(w, req, "/releases/tag/"+r.latestTag, http.StatusFound)
			return
		}
		if strings.HasPrefix(req.URL.Path, "/releases/tag/") {
			fmt.Fprintln(w, "release page")
			return
		}
		prefix := "/releases/download/" + r.tag + "/"
		if name, ok := strings.CutPrefix(req.URL.Path, prefix); ok {
			if data, ok := r.files[name]; ok {
				w.Write(data)
				return
			}
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/releases"
}

// verifier is a stub cosign or gh that logs its arguments and exits with
// code; for gh, "attestation verify --help" reports whether it has the
// attestation command.
type verifier struct {
	name        string
	code        int
	attestation bool // gh only
}

// installer runs install.sh in a sandbox whose PATH holds only the system
// tools install.sh needs and the stub verifiers, so a cosign or gh on the
// test machine never takes part.
type installer struct {
	t       *testing.T
	home    string
	bin     string // the install directory
	path    string
	logFile string
	env     []string
}

func newInstaller(t *testing.T, verifiers ...verifier) *installer {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("install.sh is for Linux and macOS")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not installed")
	}
	dir := t.TempDir()
	in := &installer{
		t:       t,
		home:    filepath.Join(dir, "home"),
		bin:     filepath.Join(dir, "home", "bin"),
		path:    filepath.Join(dir, "path"),
		logFile: filepath.Join(dir, "verifier.log"),
	}
	for _, d := range []string{in.home, in.path} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{
		"sh", "curl", "tar", "gzip", "sha256sum", "shasum", "uname", "mktemp", "rm", "mkdir",
		"chmod", "mv", "cp", "awk", "grep", "sed", "cat", "head", "tail", "tr", "id", "dirname",
		"basename", "wc", "cut",
	} {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(in.path, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, v := range verifiers {
		script := fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> %q\n", v.name, in.logFile)
		if v.name == "gh" {
			ok := 1
			if v.attestation {
				ok = 0
			}
			script += fmt.Sprintf("case \"$*\" in *--help*) exit %d ;; esac\n", ok)
		}
		script += fmt.Sprintf("exit %d\n", v.code)
		if err := os.WriteFile(filepath.Join(in.path, v.name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return in
}

// run runs install.sh against the release server at url with extra env.
func (in *installer) run(url string, env ...string) (stdout, stderr string, err error) {
	in.t.Helper()
	cmd := exec.Command(filepath.Join(in.path, "sh"), filepath.Join("..", "install.sh"))
	cmd.Env = append([]string{
		"PATH=" + in.path,
		"HOME=" + in.home,
		"KMAN_INSTALL_DIR=" + in.bin,
		"KMAN_RELEASES_URL=" + url,
	}, env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

func (in *installer) log() string {
	b, err := os.ReadFile(in.logFile)
	if err != nil && !os.IsNotExist(err) {
		in.t.Fatal(err)
	}
	return string(b)
}

// installed reports whether a kman that runs was installed.
func (in *installer) installed() bool {
	out, err := exec.Command(filepath.Join(in.bin, "kman"), "version").Output()
	return err == nil && strings.HasPrefix(string(out), "kman ")
}

func TestInstallVerifiesTheChecksumsSignatureWithCosignAgainstTheTagIdentity(t *testing.T) {
	r := newRelease(t, "v0.1.0")
	in := newInstaller(t, verifier{name: "cosign"}, verifier{name: "gh", attestation: true})
	_, stderr, err := in.run(r.serve(t))
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, stderr)
	}
	if !in.installed() {
		t.Fatal("kman was not installed")
	}
	log := in.log()
	for _, want := range []string{
		"cosign verify-blob",
		"--bundle ",
		"checksums.txt.sigstore.json",
		"--certificate-identity " + identityPrefix + "v0.1.0 ",
		"--certificate-oidc-issuer " + issuer,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("cosign was not called with %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "gh attestation verify ") {
		t.Errorf("gh verified too; one verifier is enough:\n%s", log)
	}
	if strings.Contains(stderr, "WARNING") {
		t.Errorf("a verified install warned:\n%s", stderr)
	}
}

func TestInstallVerifiesTheArchiveAttestationWithGhWhenCosignIsAbsent(t *testing.T) {
	r := newRelease(t, "v0.2.0-rc.1")
	in := newInstaller(t, verifier{name: "gh", attestation: true})
	_, stderr, err := in.run(r.serve(t), "KMAN_VERSION=v0.2.0-rc.1")
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, stderr)
	}
	if !in.installed() {
		t.Fatal("kman was not installed")
	}
	log := in.log()
	for _, want := range []string{
		"gh attestation verify ",
		r.archive,
		"--repo kirank55/kman",
		"--bundle ",
		"kman_0.2.0-rc.1.intoto.jsonl",
		"--cert-identity " + identityPrefix + "v0.2.0-rc.1 ",
		"--cert-oidc-issuer " + issuer,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("gh was not called with %q:\n%s", want, log)
		}
	}
}

func TestInstallRefusesAnArchiveWhoseChecksumDiffers(t *testing.T) {
	r := newRelease(t, "v0.1.0")
	r.files[r.archive] = append(r.files[r.archive], 0)
	in := newInstaller(t, verifier{name: "cosign"})
	_, stderr, err := in.run(r.serve(t))
	if err == nil {
		t.Fatalf("install.sh accepted a tampered archive:\n%s", stderr)
	}
	if in.installed() {
		t.Error("a tampered archive was installed")
	}
	if !strings.Contains(stderr, "checksum") {
		t.Errorf("stderr does not explain the checksum failure:\n%s", stderr)
	}
}

func TestInstallRefusesAnArchiveMissingFromChecksums(t *testing.T) {
	r := newRelease(t, "v0.1.0")
	r.files["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  kman_0.1.0_plan9_amd64.tar.gz\n")
	in := newInstaller(t, verifier{name: "cosign"})
	if _, stderr, err := in.run(r.serve(t)); err == nil {
		t.Fatalf("install.sh accepted an archive with no checksum:\n%s", stderr)
	}
	if in.installed() {
		t.Error("an archive with no checksum was installed")
	}
}

// A failed check never falls through to another verifier or to installing.
func TestInstallRefusesWhenTheSignatureDoesNotVerify(t *testing.T) {
	r := newRelease(t, "v0.1.0")
	in := newInstaller(t, verifier{name: "cosign", code: 1}, verifier{name: "gh", attestation: true})
	_, stderr, err := in.run(r.serve(t))
	if err == nil {
		t.Fatalf("install.sh accepted a bad signature:\n%s", stderr)
	}
	if in.installed() {
		t.Error("kman was installed with a bad signature")
	}
	if strings.Contains(in.log(), "gh attestation verify ") {
		t.Error("a failed cosign check fell through to gh")
	}
}

func TestInstallWarnsLoudlyWithoutAVerifier(t *testing.T) {
	r := newRelease(t, "v0.1.0")
	// A gh too old to have "gh attestation" is no verifier.
	in := newInstaller(t, verifier{name: "gh", attestation: false})
	_, stderr, err := in.run(r.serve(t))
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, stderr)
	}
	if !in.installed() {
		t.Fatal("kman was not installed")
	}
	for _, want := range []string{"WARNING", "NOT verified", "KMAN_REQUIRE_SIGNATURE=1"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestInstallFailsClosedWithoutAVerifierWhenASignatureIsRequired(t *testing.T) {
	r := newRelease(t, "v0.1.0")
	in := newInstaller(t)
	_, stderr, err := in.run(r.serve(t), "KMAN_REQUIRE_SIGNATURE=1")
	if err == nil {
		t.Fatalf("install.sh installed without a verifier although one was required:\n%s", stderr)
	}
	if in.installed() {
		t.Error("kman was installed unverified")
	}
}

func TestInstallRefusesATagThatIsNotAVersion(t *testing.T) {
	r := newRelease(t, "v0.1.0")
	in := newInstaller(t, verifier{name: "cosign"})
	if _, stderr, err := in.run(r.serve(t), "KMAN_VERSION=main"); err == nil {
		t.Fatalf("install.sh accepted KMAN_VERSION=main:\n%s", stderr)
	}
}
