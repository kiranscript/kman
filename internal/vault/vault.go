package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// KeySize is the size in bytes of the master key and of the data key.
const KeySize = 32

var (
	// ErrDecrypt means a ciphertext failed to authenticate: it was altered,
	// sealed under another data key, or moved to another id.
	ErrDecrypt = errors.New("vault: ciphertext does not decrypt")
	// ErrUnknownDataKey means a ciphertext names a data key this vault does
	// not hold.
	ErrUnknownDataKey = errors.New("vault: sealed under another data key")
	// ErrWrongMasterKey means the master key does not unwrap the data key.
	ErrWrongMasterKey = errors.New("vault: the master key does not unwrap the data key")
	// ErrClosed means the vault was closed and holds no keys any more.
	ErrClosed = errors.New("vault: closed")
)

// Options tunes how a vault is opened.
type Options struct {
	// AllowDebug skips the process hardening that otherwise runs before the
	// master key is read. It is the --allow-debug flag.
	AllowDebug bool
	// Rand is where the data key and every nonce come from. It defaults to
	// crypto/rand. The fixture generator passes a seeded one, so a rerun writes
	// the same bytes; nothing else should.
	Rand io.Reader
}

// source is opts.Rand, or crypto/rand.
func (o Options) source() io.Reader {
	if o.Rand != nil {
		return o.Rand
	}
	return rand.Reader
}

// WrappedKey is the data key as it is stored: sealed under the master key.
// It holds nothing secret without the master key.
type WrappedKey struct {
	ID          string // the dek_id credentials refer to
	Nonce       []byte
	Wrapped     []byte
	Fingerprint string // names the master key that wraps it; not secret
}

// Sealed is one secret as it is stored: encrypted under the data key and
// bound to its id.
type Sealed struct {
	DataKeyID  string
	Nonce      []byte
	Ciphertext []byte
}

// Vault holds the unwrapped data key and one decrypted copy of each
// credential it has been given.
type Vault struct {
	dekID string
	rand  io.Reader

	mu    sync.Mutex // serializes writers; Credential takes no lock
	aead  cipher.AEAD
	cache atomic.Pointer[map[string][]byte]
}

// Create makes a fresh data key with id dekID, wraps it under the master key
// from src, and returns a vault holding it with the wrapped form to store.
// The wrapping binds dekID, which the caller mints with store.NewID so it
// has the store's id form. The master key is wiped before Create returns.
func Create(src KeySource, dekID string, opts Options) (*Vault, WrappedKey, error) {
	if dekID == "" {
		return nil, WrappedKey{}, errors.New("vault: no data-key id")
	}
	mk, err := readMasterKey(src, opts)
	if err != nil {
		return nil, WrappedKey{}, err
	}
	defer clear(mk)
	dek := make([]byte, KeySize)
	defer clear(dek)
	if _, err := io.ReadFull(opts.source(), dek); err != nil {
		return nil, WrappedKey{}, fmt.Errorf("vault: random data key: %w", err)
	}
	wk := WrappedKey{ID: dekID, Fingerprint: fingerprint(mk)}
	kek, err := newAEAD(mk)
	if err != nil {
		return nil, WrappedKey{}, err
	}
	wk.Nonce = make([]byte, kek.NonceSize())
	if _, err := io.ReadFull(opts.source(), wk.Nonce); err != nil {
		return nil, WrappedKey{}, fmt.Errorf("vault: random nonce: %w", err)
	}
	wk.Wrapped = kek.Seal(nil, wk.Nonce, dek, wrapAAD(wk.ID))
	v, err := newVault(wk.ID, dek, opts.source())
	return v, wk, err
}

// Unlock reads the master key from src, unwraps the data key with it, and
// wipes the master key. It fails with ErrWrongMasterKey when the master key
// is not the one that wrapped wk.
func Unlock(src KeySource, wk WrappedKey, opts Options) (*Vault, error) {
	mk, err := readMasterKey(src, opts)
	if err != nil {
		return nil, err
	}
	defer clear(mk)
	if !hmac.Equal([]byte(fingerprint(mk)), []byte(wk.Fingerprint)) {
		return nil, fmt.Errorf("unlock with the master key from %s: %w", src, ErrWrongMasterKey)
	}
	kek, err := newAEAD(mk)
	if err != nil {
		return nil, err
	}
	dek, err := open(kek, wk.Nonce, wk.Wrapped, wrapAAD(wk.ID))
	if err != nil {
		return nil, fmt.Errorf("unlock with the master key from %s: %w", src, ErrWrongMasterKey)
	}
	defer clear(dek)
	return newVault(wk.ID, dek, opts.source())
}

func readMasterKey(src KeySource, opts Options) ([]byte, error) {
	if err := hardenFirst(opts); err != nil {
		return nil, err
	}
	mk, err := src.MasterKey()
	if err != nil {
		return nil, fmt.Errorf("read the master key from %s: %w", src, err)
	}
	if len(mk) != KeySize {
		clear(mk)
		return nil, fmt.Errorf("the master key from %s is %d bytes, want %d", src, len(mk), KeySize)
	}
	return mk, nil
}

func newVault(dekID string, dek []byte, random io.Reader) (*Vault, error) {
	aead, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	v := &Vault{dekID: dekID, rand: random, aead: aead}
	v.cache.Store(&map[string][]byte{})
	return v, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// open is aead.Open that fails, instead of panicking, on a nonce of the
// wrong length, as a corrupt row would have.
func open(aead cipher.AEAD, nonce, ciphertext, aad []byte) ([]byte, error) {
	if len(nonce) != aead.NonceSize() {
		return nil, ErrDecrypt
	}
	return aead.Open(nil, nonce, ciphertext, aad)
}

// wrapAAD binds a wrapped data key to its id.
func wrapAAD(dekID string) []byte { return []byte("kman data key v1 " + dekID) }

// fingerprint names a master key without revealing it: a keyed hash under
// the key itself, so equal keys match and the value says nothing about it.
func fingerprint(mk []byte) string {
	m := hmac.New(sha256.New, mk)
	m.Write([]byte("kman master key fingerprint v1"))
	return "mkf_" + hex.EncodeToString(m.Sum(nil)[:16])
}

// DataKeyID returns the id of the data key this vault seals under.
func (v *Vault) DataKeyID() string { return v.dekID }

// Seal encrypts plaintext under the data key, bound to id as AAD. It does
// not cache the plaintext: call Put once the sealed form is stored.
func (v *Vault) Seal(id string, plaintext []byte) (Sealed, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.aead == nil {
		return Sealed{}, ErrClosed
	}
	s := Sealed{DataKeyID: v.dekID, Nonce: make([]byte, v.aead.NonceSize())}
	if _, err := io.ReadFull(v.rand, s.Nonce); err != nil {
		return Sealed{}, fmt.Errorf("vault: random nonce: %w", err)
	}
	s.Ciphertext = v.aead.Seal(nil, s.Nonce, plaintext, []byte(id))
	return s, nil
}

// Put decrypts s as the secret with id and caches the plaintext. A secret
// already cached under id is replaced and its plaintext wiped, which is how
// rotation reaches memory.
func (v *Vault) Put(id string, s Sealed) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.aead == nil {
		return ErrClosed
	}
	if s.DataKeyID != v.dekID {
		return fmt.Errorf("credential %s names data key %q, this vault holds %q: %w", id, s.DataKeyID, v.dekID, ErrUnknownDataKey)
	}
	plain, err := open(v.aead, s.Nonce, s.Ciphertext, []byte(id))
	if err != nil {
		return fmt.Errorf("credential %s: %w", id, ErrDecrypt)
	}
	v.swap(id, plain)
	return nil
}

// Delete wipes the cached plaintext of the secret with id, if there is one.
func (v *Vault) Delete(id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.swap(id, nil)
}

// swap publishes a copy of the cache with id set to plain, or removed when
// plain is nil, then wipes the plaintext it replaced. The caller holds mu.
func (v *Vault) swap(id string, plain []byte) {
	old := *v.cache.Load()
	next := make(map[string][]byte, len(old)+1)
	for k, b := range old {
		next[k] = b
	}
	if plain == nil {
		delete(next, id)
	} else {
		next[id] = plain
	}
	v.cache.Store(&next)
	clear(old[id])
}

// Credential returns the cached plaintext of the secret with id. It takes
// no lock, so the proxy can call it on every request. The slice is the one
// cached copy: copy what you need from it at once, never modify or keep it,
// because rotation and delete wipe it.
func (v *Vault) Credential(id string) ([]byte, bool) {
	b, ok := (*v.cache.Load())[id]
	return b, ok
}

// Close wipes every cached plaintext and drops the data key.
func (v *Vault) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	old := *v.cache.Swap(&map[string][]byte{})
	for _, b := range old {
		clear(b)
	}
	v.aead = nil
}
