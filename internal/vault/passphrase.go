package vault

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/argon2"
)

// ErrWrongPassphrase means the passphrase does not unseal the key file.
var ErrWrongPassphrase = errors.New("vault: the passphrase does not unseal the master key")

const sealedFormat = "kman-sealed-master-key"

// sealedKey is a passphrase-sealed key file: the master key encrypted with
// AES-256-GCM under a key derived from the passphrase with Argon2id.
type sealedKey struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Time       uint32 `json:"time"`
	MemoryKiB  uint32 `json:"memory_kib"`
	Threads    uint8  `json:"threads"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// The second recommended option of RFC 9106 §4: t=3, p=4, 64 MiB. It
// costs tens of milliseconds, once per start.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	// maxArgonMemory bounds what a key file may ask for, so a damaged file
	// cannot make kman allocate without limit.
	maxArgonMemory = 4 * 1024 * 1024
)

// SealMasterKey seals mk under passphrase and returns the content of a key
// file for PassphraseSource. It does not wipe its arguments.
func SealMasterKey(mk, passphrase []byte) ([]byte, error) {
	if len(mk) != KeySize {
		return nil, fmt.Errorf("a master key is %d bytes, not %d", KeySize, len(mk))
	}
	if len(passphrase) == 0 {
		return nil, errors.New("the passphrase is empty")
	}
	s := sealedKey{
		Format: sealedFormat, Version: 1, KDF: "argon2id",
		Time: argonTime, MemoryKiB: argonMemory, Threads: argonThreads,
		Salt: make([]byte, 16),
	}
	rand.Read(s.Salt)
	aead, err := s.aead(passphrase)
	if err != nil {
		return nil, err
	}
	s.Nonce = make([]byte, aead.NonceSize())
	rand.Read(s.Nonce)
	s.Ciphertext = aead.Seal(nil, s.Nonce, mk, s.aad())
	out, err := json.MarshalIndent(s, "", "  ")
	return append(out, '\n'), err
}

// aead derives the key-file key from passphrase.
func (s sealedKey) aead(passphrase []byte) (cipher.AEAD, error) {
	k := argon2.IDKey(passphrase, s.Salt, s.Time, s.MemoryKiB, s.Threads, KeySize)
	defer clear(k)
	return newAEAD(k)
}

// aad binds the ciphertext to the KDF parameters, so changing them in the
// file fails to decrypt instead of deriving another key.
func (s sealedKey) aad() []byte {
	return fmt.Appendf(nil, "%s v%d %s t=%d m=%d p=%d", s.Format, s.Version, s.KDF, s.Time, s.MemoryKiB, s.Threads)
}

func (s sealedKey) check() error {
	switch {
	case s.Format != sealedFormat:
		return fmt.Errorf("it is not a passphrase-sealed key file (format %q)", s.Format)
	case s.Version != 1 || s.KDF != "argon2id":
		return fmt.Errorf("it is version %d with KDF %q; this kman reads version 1 with argon2id", s.Version, s.KDF)
	case s.Time < 1 || s.Time > 100, s.Threads < 1,
		s.MemoryKiB < 8*uint32(s.Threads), s.MemoryKiB > maxArgonMemory,
		len(s.Salt) < 16:
		return errors.New("its Argon2id parameters are out of range")
	}
	return nil
}

type passphraseSource struct {
	path   string
	prompt func() ([]byte, error)
}

// PassphraseSource reads a key file made by SealMasterKey and unseals it
// with a passphrase from prompt, which is called on every read: every
// start. It wipes the passphrase after use.
func PassphraseSource(path string, prompt func() ([]byte, error)) KeySource {
	return passphraseSource{path, prompt}
}

func (s passphraseSource) String() string { return "passphrase-sealed key file " + s.path }

func (s passphraseSource) MasterKey() ([]byte, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var sk sealedKey
	if err := json.Unmarshal(raw, &sk); err != nil {
		return nil, fmt.Errorf("%s: it is not a passphrase-sealed key file", s.path)
	}
	if err := sk.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	pass, err := s.prompt()
	defer clear(pass)
	if err != nil {
		return nil, fmt.Errorf("read the passphrase: %w", err)
	}
	if len(pass) == 0 {
		return nil, errors.New("the passphrase is empty")
	}
	aead, err := sk.aead(pass)
	if err != nil {
		return nil, err
	}
	mk, err := open(aead, sk.Nonce, sk.Ciphertext, sk.aad())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, ErrWrongPassphrase)
	}
	return mk, nil
}
