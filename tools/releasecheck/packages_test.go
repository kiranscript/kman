package releasecheck_test

import (
	"slices"
	"strings"
	"testing"
)

// unit is a systemd unit file: section → key → values, in file order.
type unit map[string]map[string][]string

func loadUnit(t *testing.T, rel string) unit {
	t.Helper()
	u := unit{}
	section := ""
	for n, line := range strings.Split(string(read(t, rel)), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = line[1 : len(line)-1]
			u[section] = map[string][]string{}
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok || section == "" {
				t.Fatalf("%s:%d: %q is not a key=value in a section", rel, n+1, line)
			}
			u[section][k] = append(u[section][k], v)
		}
	}
	return u
}

// one is the single value of key in section, failing if it is unset or set
// more than once.
func (u unit) one(t *testing.T, section, key string) string {
	t.Helper()
	v := u[section][key]
	if len(v) != 1 {
		t.Errorf("[%s] %s = %v, want exactly one value", section, key, v)
		return ""
	}
	return v[0]
}

const unitFile = "packaging/kman.service"

// SPEC §3: the packaged unit runs as a dynamic user, with systemd owning the
// state and config directories and the master key an encrypted credential.
func TestTheUnitRunsKmanAsADynamicUserWithAnEncryptedKey(t *testing.T) {
	u := loadUnit(t, unitFile)
	for key, want := range map[string]string{
		"DynamicUser":            "yes",
		"StateDirectory":         "kman",
		"ConfigurationDirectory": "kman",
		// The credential name is vault.SystemdCredName.
		"LoadCredentialEncrypted": "master.key:/etc/kman/master.key.cred",
	} {
		if got := u.one(t, "Service", key); got != want {
			t.Errorf("[Service] %s = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"User", "Group"} {
		if v, ok := u["Service"][key]; ok {
			t.Errorf("[Service] %s = %v; DynamicUser picks the user", key, v)
		}
	}
	if got := u.one(t, "Install", "WantedBy"); got != "multi-user.target" {
		t.Errorf("[Install] WantedBy = %q, want multi-user.target", got)
	}
}

// init writes /etc/kman/kman.yaml 0600 as root, which the dynamic user
// cannot read. systemd hands it to the service as a credential instead, so
// serve reads its config from the credentials directory (%d).
func TestTheUnitServesTheConfigFromTheCredentialsDirectory(t *testing.T) {
	u := loadUnit(t, unitFile)
	if got := u.one(t, "Service", "LoadCredential"); got != "kman.yaml:/etc/kman/kman.yaml" {
		t.Errorf("[Service] LoadCredential = %q, want kman.yaml:/etc/kman/kman.yaml", got)
	}
	if got := u.one(t, "Service", "ExecStart"); got != "/usr/bin/kman serve --config-dir %d" {
		t.Errorf("[Service] ExecStart = %q, want /usr/bin/kman serve --config-dir %%d", got)
	}
}

// Nothing in the unit may hand the service a way back to root or to write
// outside its state directory.
func TestTheUnitIsSandboxed(t *testing.T) {
	u := loadUnit(t, unitFile)
	for key, want := range map[string]string{
		"CapabilityBoundingSet": "",
		"ProtectSystem":         "strict",
		"NoNewPrivileges":       "yes",
		"PrivateDevices":        "yes",
		"LimitCORE":             "0",
	} {
		if got := u.one(t, "Service", key); got != want {
			t.Errorf("[Service] %s = %q, want %q", key, got, want)
		}
	}
	if fams := strings.Fields(u.one(t, "Service", "RestrictAddressFamilies")); !slices.Equal(fams, []string{"AF_INET", "AF_INET6", "AF_UNIX"}) {
		t.Errorf("[Service] RestrictAddressFamilies = %v, want AF_INET AF_INET6 AF_UNIX", fams)
	}
}

type nfpm struct {
	ID               string
	IDs              []string `yaml:"ids"`
	PackageName      string   `yaml:"package_name"`
	FileNameTemplate string   `yaml:"file_name_template"`
	Formats          []string
	Bindir           string
	Mtime            string
	Contents         []nfpmContent
	Scripts          map[string]any
	Overrides        map[string]map[string]any
	RPM              struct {
		BuildHost string `yaml:"buildhost"`
		Scripts   map[string]any
	} `yaml:"rpm"`
	Deb struct {
		Scripts  map[string]any
		Triggers map[string]any
	}
}

type nfpmContent struct {
	Src, Dst, Type string
	FileInfo       struct {
		Mode         int
		Owner, Group string
		Mtime        string
	} `yaml:"file_info"`
}

func loadNfpm(t *testing.T) nfpm {
	t.Helper()
	g := loadGoreleaser(t)
	if len(g.Nfpms) != 1 {
		t.Fatalf("got %d nfpms, want one", len(g.Nfpms))
	}
	return g.Nfpms[0]
}

// SPEC §3: a .deb and an .rpm of the released linux binary, shipping
// kman.service.
func TestTheDebAndRPMShipTheBinaryAndTheUnit(t *testing.T) {
	p := loadNfpm(t)
	if f := slices.Sorted(slices.Values(p.Formats)); !slices.Equal(f, []string{"deb", "rpm"}) {
		t.Errorf("formats = %v, want deb and rpm", p.Formats)
	}
	if !slices.Equal(p.IDs, []string{"kman"}) || p.Bindir != "/usr/bin" {
		t.Errorf("ids %v into %q, want the kman build into /usr/bin (the unit's ExecStart)", p.IDs, p.Bindir)
	}
	if p.PackageName != "kman" {
		t.Errorf("package_name = %q, want kman", p.PackageName)
	}
	// deb and rpm have their own naming rules (kman_0.1.0_amd64.deb,
	// kman-0.1.0-1.x86_64.rpm); the archive names stay as they are.
	if p.FileNameTemplate != "{{ .ConventionalFileName }}" {
		t.Errorf("file_name_template = %q, want {{ .ConventionalFileName }}", p.FileNameTemplate)
	}
	var unitDst []string
	for _, c := range p.Contents {
		if c.Src == unitFile {
			unitDst = append(unitDst, c.Dst)
			if c.FileInfo.Mode != 0o644 {
				t.Errorf("%s mode = %o, want 0644", c.Dst, c.FileInfo.Mode)
			}
		}
	}
	if !slices.Equal(unitDst, []string{"/usr/lib/systemd/system/kman.service"}) {
		t.Errorf("%s is installed at %v, want /usr/lib/systemd/system/kman.service", unitFile, unitDst)
	}
}

// SPEC §3: the package scripts create nothing. systemd makes the user and
// the directories when the unit starts, so the packages carry no scripts
// and no directories, only files.
func TestThePackagesHaveNoScriptsAndCreateNothing(t *testing.T) {
	p := loadNfpm(t)
	if len(p.Scripts) != 0 || len(p.RPM.Scripts) != 0 || len(p.Deb.Scripts) != 0 || len(p.Deb.Triggers) != 0 {
		t.Errorf("scripts %v, rpm %v, deb %v %v: the packages must carry none", p.Scripts, p.RPM.Scripts, p.Deb.Scripts, p.Deb.Triggers)
	}
	for format, o := range p.Overrides {
		for _, key := range []string{"scripts", "contents"} {
			if _, ok := o[key]; ok {
				t.Errorf("overrides.%s.%s: every format ships the same files and no scripts", format, key)
			}
		}
	}
	for _, c := range p.Contents {
		if c.Type != "" && c.Type != "config" && c.Type != "config|noreplace" {
			t.Errorf("content %s has type %q; the packages ship files only", c.Dst, c.Type)
		}
		if strings.HasPrefix(c.Dst, "/etc/") || strings.HasPrefix(c.Dst, "/var/") {
			t.Errorf("content %s: /etc/kman and /var/lib/kman belong to systemd and kman init", c.Dst)
		}
	}
}

// The rebuild job compares every rebuilt file with the release, packages
// included, so the packages take their times from the commit and the RPM a
// fixed build host (nfpm defaults to the runner's hostname).
func TestThePackagesAreReproducible(t *testing.T) {
	p := loadNfpm(t)
	if p.Mtime != "{{ .CommitDate }}" {
		t.Errorf("mtime = %q, want {{ .CommitDate }}", p.Mtime)
	}
	if p.RPM.BuildHost == "" || strings.Contains(p.RPM.BuildHost, "{{") {
		t.Errorf("rpm.buildhost = %q, want a fixed name", p.RPM.BuildHost)
	}
	for _, c := range p.Contents {
		if c.FileInfo.Mtime != "{{ .CommitDate }}" || c.FileInfo.Owner != "root" || c.FileInfo.Group != "root" {
			t.Errorf("content %s file_info = %+v, want root:root at {{ .CommitDate }}", c.Dst, c.FileInfo)
		}
	}
}

// The snapshot dry run checks every best-effort channel it built, and runs
// whenever one of their files changes.
func TestTheSnapshotRunsTheDocumentedSetupOfEveryPackage(t *testing.T) {
	w := loadWorkflow(t, "release-snapshot.yml")
	paths := map[string]bool{}
	for _, p := range w.On["pull_request"].(map[string]any)["paths"].([]any) {
		paths[p.(string)] = true
	}
	for _, want := range []string{"packaging/**", "examples/compose/**", "docs/operator-guide/docker.md", "docs/operator-guide/systemd.md"} {
		if !paths[want] {
			t.Errorf("release-snapshot.yml paths lack %s", want)
		}
	}
	var script string
	for _, s := range w.Jobs["snapshot"].Steps {
		script += s.Run + "\n"
	}
	for _, want := range []string{
		"sh scripts/doc-steps.sh docs/operator-guide/docker.md compose",
		"sh scripts/doc-steps.sh docs/operator-guide/systemd.md systemd",
		"sh scripts/homebrew-formula.sh",
		"ruby -c",
		`grep -c '\.deb$' dist/checksums.txt`,
		`grep -c '\.rpm$' dist/checksums.txt`,
		"systemd-analyze verify",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the snapshot job does not run %q", want)
		}
	}
}
