package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// KeySource is where kman obtains the master key at startup (ADR 0003).
type KeySource interface {
	// MasterKey returns the master key in a fresh buffer that the caller
	// owns and wipes.
	MasterKey() ([]byte, error)
	// String names the source for errors and logs. It never holds the key.
	String() string
}

// NewMasterKey returns a fresh random master key. The caller wipes it.
func NewMasterKey() []byte {
	mk := make([]byte, KeySize)
	rand.Read(mk)
	return mk
}

// EncodeMasterKey returns the master key's text form, one line of standard
// base64: what kman keygen prints and what a key file, a systemd credential
// or KMAN_MASTER_KEY holds. The caller wipes it.
func EncodeMasterKey(mk []byte) []byte {
	enc := base64.StdEncoding
	out := make([]byte, enc.EncodedLen(len(mk))+1)
	enc.Encode(out, mk)
	out[len(out)-1] = '\n'
	return out
}

var errKeyText = errors.New("it does not hold a master key: want one line of base64 from kman keygen")

// decodeMasterKey parses a master key's text form and wipes text. Its
// errors never quote the text.
func decodeMasterKey(text []byte) ([]byte, error) {
	defer clear(text)
	enc := base64.StdEncoding
	trimmed := bytes.TrimSpace(text)
	mk := make([]byte, enc.DecodedLen(len(trimmed)))
	n, err := enc.Decode(mk, trimmed)
	if err != nil || n != KeySize {
		clear(mk)
		return nil, errKeyText
	}
	return mk[:n], nil
}

type fileSource struct{ path string }

// FileSource reads the master key from a file holding its text form. It is
// the default source, and it covers Docker and Kubernetes secret mounts.
func FileSource(path string) KeySource { return fileSource{path} }

func (s fileSource) String() string { return "key file " + s.path }

func (s fileSource) MasterKey() ([]byte, error) {
	text, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	mk, err := decodeMasterKey(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return mk, nil
}

// SystemdCredName is the credential name the systemd-creds source reads,
// as in LoadCredentialEncrypted=master.key:/etc/kman/master.key.cred.
const SystemdCredName = "master.key"

type systemdSource struct{}

// SystemdCredsSource reads the master key from the credential systemd
// decrypted into $CREDENTIALS_DIRECTORY for this service.
func SystemdCredsSource() KeySource { return systemdSource{} }

func (systemdSource) String() string { return "systemd credential " + SystemdCredName }

func (systemdSource) MasterKey() ([]byte, error) {
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return nil, fmt.Errorf("$CREDENTIALS_DIRECTORY is not set: run kman as a systemd service with LoadCredentialEncrypted=%s:<file made by systemd-creds encrypt>", SystemdCredName)
	}
	return fileSource{filepath.Join(dir, SystemdCredName)}.MasterKey()
}

// EnvVar is the environment variable the env source reads.
const EnvVar = "KMAN_MASTER_KEY"

type envSource struct{ warn io.Writer }

// EnvSource reads the master key from $KMAN_MASTER_KEY and writes a warning
// to warn on every read, which is every start.
func EnvSource(warn io.Writer) KeySource { return envSource{warn} }

func (envSource) String() string { return "$" + EnvVar }

func (s envSource) MasterKey() ([]byte, error) {
	fmt.Fprintf(s.warn, "kman: warning: the master key comes from $%s, where any process that reads this one's environment before it is hardened can see it, and child processes inherit it. Prefer a key file or systemd-creds.\n", EnvVar)
	text := os.Getenv(EnvVar)
	if text == "" {
		return nil, fmt.Errorf("$%s is not set", EnvVar)
	}
	mk, err := decodeMasterKey([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("$%s: %w", EnvVar, err)
	}
	return mk, nil
}
