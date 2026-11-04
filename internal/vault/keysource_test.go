package vault_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/vault"
)

// keyText is the master key's text form: what kman keygen prints and a key
// file holds.
var keyText = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=\n$`)

func TestAMasterKeyIsWrittenAsOneLineOfBase64(t *testing.T) {
	mk := vault.NewMasterKey()
	text := vault.EncodeMasterKey(mk)
	if !keyText.Match(text) {
		t.Fatalf("EncodeMasterKey = %q, want 44 base64 characters and a newline", text)
	}
	if bytes.Equal(vault.NewMasterKey(), mk) {
		t.Fatal("two new master keys are equal")
	}
}

func writeFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheFileSourceReadsAKeyFile(t *testing.T) {
	mk := vault.NewMasterKey()
	text := vault.EncodeMasterKey(mk)
	for name, content := range map[string][]byte{
		"as written":    text,
		"no newline":    bytes.TrimSpace(text),
		"CRLF":          append(bytes.TrimSpace(text), '\r', '\n'),
		"padded spaces": append(append([]byte("  "), bytes.TrimSpace(text)...), " \n\n"...),
	} {
		src := vault.FileSource(writeFile(t, content))
		got, err := src.MasterKey()
		if err != nil || !bytes.Equal(got, mk) {
			t.Errorf("%s: MasterKey = %x, %v; want the written key", name, got, err)
		}
	}
}

func TestTheFileSourceGivesAFreshBufferEachRead(t *testing.T) {
	src := vault.FileSource(writeFile(t, vault.EncodeMasterKey(vault.NewMasterKey())))
	a, _ := src.MasterKey()
	b, _ := src.MasterKey()
	clear(a)
	if allZero(b) {
		t.Fatal("wiping one read wiped the next")
	}
}

func TestTheFileSourceNamesThePathAndNeverTheContent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.key")
	if _, err := vault.FileSource(missing).MasterKey(); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("missing file: err = %v, want it to name %s", err, missing)
	}
	if s := vault.FileSource(missing).String(); !strings.Contains(s, missing) {
		t.Errorf("String() = %q, want it to name the path", s)
	}
	for name, content := range map[string]string{
		"not base64": "sk-not-a-master-key-at-all-but-a-secret!!!!\n",
		"too short":  "c2stc2hvcnQtc2VjcmV0\n",
		"two keys":   strings.Repeat(string(bytes.TrimSpace(vault.EncodeMasterKey(vault.NewMasterKey()))), 2),
		"empty":      "",
	} {
		_, err := vault.FileSource(writeFile(t, []byte(content))).MasterKey()
		if err == nil {
			t.Errorf("%s: MasterKey succeeded, want an error", name)
			continue
		}
		if c := strings.TrimSpace(content); c != "" && strings.Contains(err.Error(), c[:12]) {
			t.Errorf("%s: the error %q shows the file's content", name, err)
		}
	}
}

func TestTheSystemdCredsSourceReadsTheCredentialsDirectory(t *testing.T) {
	mk := vault.NewMasterKey()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, vault.SystemdCredName), vault.EncodeMasterKey(mk), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)
	got, err := vault.SystemdCredsSource().MasterKey()
	if err != nil || !bytes.Equal(got, mk) {
		t.Fatalf("MasterKey = %x, %v; want the credential", got, err)
	}
}

func TestTheSystemdCredsSourceExplainsAMissingCredential(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	_, err := vault.SystemdCredsSource().MasterKey()
	if err == nil || !strings.Contains(err.Error(), "CREDENTIALS_DIRECTORY") || !strings.Contains(err.Error(), "LoadCredentialEncrypted") {
		t.Errorf("unset: err = %v, want it to name $CREDENTIALS_DIRECTORY and LoadCredentialEncrypted=", err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", t.TempDir())
	if _, err := vault.SystemdCredsSource().MasterKey(); err == nil || !strings.Contains(err.Error(), vault.SystemdCredName) {
		t.Errorf("no credential: err = %v, want it to name %s", err, vault.SystemdCredName)
	}
}

func TestTheEnvSourceReadsKMANMasterKeyAndWarnsEveryTime(t *testing.T) {
	mk := vault.NewMasterKey()
	t.Setenv("KMAN_MASTER_KEY", string(vault.EncodeMasterKey(mk)))
	var warn bytes.Buffer
	src := vault.EnvSource(&warn)
	for range 2 {
		got, err := src.MasterKey()
		if err != nil || !bytes.Equal(got, mk) {
			t.Fatalf("MasterKey = %x, %v; want the key from the environment", got, err)
		}
	}
	if n := strings.Count(warn.String(), "warning: "); n != 2 {
		t.Errorf("warned %d times in two reads, want 2:\n%s", n, warn.String())
	}
	if !strings.Contains(warn.String(), "KMAN_MASTER_KEY") {
		t.Errorf("the warning %q does not name KMAN_MASTER_KEY", warn.String())
	}
	if strings.Contains(warn.String(), strings.TrimSpace(string(vault.EncodeMasterKey(mk)))) {
		t.Error("the warning shows the key")
	}
}

func TestTheEnvSourceRefusesAnUnsetOrBadVariable(t *testing.T) {
	t.Setenv("KMAN_MASTER_KEY", "")
	if _, err := vault.EnvSource(&bytes.Buffer{}).MasterKey(); err == nil || !strings.Contains(err.Error(), "KMAN_MASTER_KEY") {
		t.Errorf("unset: err = %v, want it to name KMAN_MASTER_KEY", err)
	}
	t.Setenv("KMAN_MASTER_KEY", "sk-not-a-master-key-at-all")
	if _, err := vault.EnvSource(&bytes.Buffer{}).MasterKey(); err == nil || strings.Contains(err.Error(), "sk-not-a-master") {
		t.Errorf("bad value: err = %v, want an error that does not quote it", err)
	}
}
