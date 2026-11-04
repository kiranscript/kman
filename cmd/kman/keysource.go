package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirank55/kman/internal/vault"
)

// keySource turns the config's key_source into a vault key source. prompt
// asks for the passphrase of the passphrase source, on every start.
func keySource(c Config, warn io.Writer, prompt func() ([]byte, error)) (vault.KeySource, error) {
	switch c.KeySource {
	case keySourceFile:
		return vault.FileSource(c.MasterKeyFile), nil
	case keySourceSystemd:
		return vault.SystemdCredsSource(), nil
	case keySourcePassphrase:
		return vault.PassphraseSource(c.MasterKeyFile, prompt), nil
	case keySourceEnv:
		return vault.EnvSource(warn), nil
	}
	return nil, fmt.Errorf("key_source %q is not one of file, systemd-creds, passphrase, env", c.KeySource)
}

// errKeyInDataDir is invariant 1 (ADR 0003): a backup or a copied volume of
// the data directory must never carry the key that decrypts it.
var errKeyInDataDir = errors.New("the master key file is inside the data directory, so anything that copies the data directory copies the key that decrypts it")

// inside reports whether path is dir or lies under it, after following
// symlinks in whichever of the two exist.
func inside(dir, path string) (bool, error) {
	d, err := resolve(dir)
	if err != nil {
		return false, err
	}
	p, err := resolve(path)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(d, p)
	if err != nil {
		return false, nil // different volumes
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

// resolve is path made absolute with symlinks followed, as far as it exists.
func resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var rest []string
	for p := abs; ; {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return abs, nil
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}
