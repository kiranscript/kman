package vault_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/vault"
)

// TestTheSystemdCredsSourceUnderSystemd encrypts a master key with
// systemd-creds, then runs this test binary as a transient service with
// LoadCredentialEncrypted=, where TestSystemdCredsHelper opens a vault from
// the systemd-creds source. It needs root and systemd as PID 1, so it runs
// only when KMAN_TEST_SYSTEMD=1: CI's systemd-creds job sets that inside a
// container booted with systemd.
func TestTheSystemdCredsSourceUnderSystemd(t *testing.T) {
	if os.Getenv("KMAN_TEST_SYSTEMD") != "1" {
		t.Skip("set KMAN_TEST_SYSTEMD=1 to run under systemd (needs root and systemd as PID 1)")
	}
	dir, err := os.MkdirTemp("/var/tmp", "kman-systemd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	src := newFixedSource(t)
	_, want := create(t, src)
	plain := filepath.Join(dir, "master.key")
	cred := filepath.Join(dir, "master.key.cred")
	if err := os.WriteFile(plain, vault.EncodeMasterKey(src.key), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, "systemd-creds", "encrypt", "--with-key=host", "--name="+vault.SystemdCredName, plain, cred)
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := run(t, "systemd-run", "--wait", "--pipe", "--quiet", "--collect",
		"-p", fmt.Sprintf("LoadCredentialEncrypted=%s:%s", vault.SystemdCredName, cred),
		"-E", "KMAN_TEST_SYSTEMD_HELPER=1",
		self, "-test.run=^TestSystemdCredsHelper$", "-test.v")
	if !strings.Contains(out, "fingerprint="+want.Fingerprint+"\n") {
		t.Fatalf("the service did not unlock with the encrypted credential; want fingerprint=%s in:\n%s", want.Fingerprint, out)
	}
}

// TestSystemdCredsHelper is the service side of the test above.
func TestSystemdCredsHelper(t *testing.T) {
	if os.Getenv("KMAN_TEST_SYSTEMD_HELPER") != "1" {
		t.Skip("runs only inside the systemd service of TestTheSystemdCredsSourceUnderSystemd")
	}
	if os.Getenv("CREDENTIALS_DIRECTORY") == "" {
		t.Fatal("systemd set no $CREDENTIALS_DIRECTORY")
	}
	v, wk, err := vault.Create(vault.SystemdCredsSource(), vault.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v.Close()
	fmt.Printf("fingerprint=%s\n", wk.Fingerprint)
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}
