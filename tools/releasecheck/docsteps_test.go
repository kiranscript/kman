package releasecheck_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docSteps runs scripts/doc-steps.sh on a doc and returns what it prints.
func docSteps(t *testing.T, doc, name string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join(root, "scripts", "doc-steps.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", script, doc, name).Output()
	return string(out), err
}

const stepsDoc = "# Install\n\n```sh\necho not-a-step\n```\n\n" +
	"<!-- install:begin -->\n\nFirst:\n\n```sh\necho one\n```\n\nThen:\n\n```sh\necho two\n```\n\n<!-- install:end -->\n\n" +
	"<!-- other:begin -->\n```sh\necho other\n```\n<!-- other:end -->\n"

// CI runs a doc's steps exactly as written: the sh blocks between
// <!-- name:begin --> and <!-- name:end -->.
func TestDocStepsPrintsTheMarkedBlocksOnly(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "install.md")
	writeFile(t, doc, stepsDoc)
	got, err := docSteps(t, doc, "install")
	if err != nil {
		t.Fatal(err)
	}
	if want := "echo one\necho two\n"; got != want {
		t.Errorf("steps = %q, want %q", got, want)
	}
}

func TestDocStepsFailsWithoutTheBlock(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "install.md")
	writeFile(t, doc, stepsDoc)
	if got, err := docSteps(t, doc, "missing"); err == nil {
		t.Errorf("printed %q for a block the doc lacks", got)
	}
}

// SPEC §3's Docker steps, as the operator guide gives them and as the
// snapshot job runs them.
func TestTheDockerGuideGivesTheSpecsDockerSteps(t *testing.T) {
	got, err := docSteps(t, filepath.Join(root, "docs", "operator-guide", "docker.md"), "compose")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"docker run --rm ghcr.io/kirank55/kman keygen > master.key",
		"docker compose run --rm kman init",
		"docker compose up -d",
	}, "\n") + "\n"
	if got != want {
		t.Errorf("compose steps = %q, want SPEC §3's %q", got, want)
	}
}

// The systemd guide's steps set up what the packaged unit expects: the
// encrypted key where LoadCredentialEncrypted= reads it, and kman.yaml and
// the data directory where the unit and systemd keep them.
func TestTheSystemdGuideSetsUpWhatTheUnitExpects(t *testing.T) {
	steps, err := docSteps(t, filepath.Join(root, "docs", "operator-guide", "systemd.md"), "systemd")
	if err != nil {
		t.Fatal(err)
	}
	u := loadUnit(t, unitFile)
	_, cred, _ := strings.Cut(u.one(t, "Service", "LoadCredentialEncrypted"), ":")
	for _, want := range []string{
		"systemd-creds encrypt --name=master.key - " + cred,
		"-p LoadCredentialEncrypted=master.key:" + cred,
		"kman init --config-dir /etc/kman --data-dir /var/lib/kman --key-source systemd-creds --no-save-token",
		"systemctl enable --now kman",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("the systemd steps lack %q:\n%s", want, steps)
		}
	}
	if strings.Contains(steps, "sudo") {
		t.Error("the systemd steps run as root, as CI runs them; say so in the text instead of sudo")
	}
}
