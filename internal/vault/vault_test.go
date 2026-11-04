package vault_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/vault"
)

// fixedSource is a key source that hands out a copy of one master key and
// remembers every buffer it handed out, so a test can check they were wiped.
type fixedSource struct {
	key    []byte
	issued [][]byte
	reads  int
}

func newFixedSource(t *testing.T) *fixedSource {
	t.Helper()
	return &fixedSource{key: vault.NewMasterKey()}
}

func (s *fixedSource) MasterKey() ([]byte, error) {
	s.reads++
	k := bytes.Clone(s.key)
	s.issued = append(s.issued, k)
	return k, nil
}

func (s *fixedSource) String() string { return "test source" }

// create makes a vault on a fresh data key and returns it with its wrapped
// data key.
func create(t *testing.T, src vault.KeySource) (*vault.Vault, vault.WrappedKey) {
	t.Helper()
	v, wk, err := vault.Create(src, vault.Options{AllowDebug: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(v.Close)
	return v, wk
}

func unlock(t *testing.T, src vault.KeySource, wk vault.WrappedKey) *vault.Vault {
	t.Helper()
	v, err := vault.Unlock(src, wk, vault.Options{AllowDebug: true})
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	t.Cleanup(v.Close)
	return v
}

func TestACredentialSealedAtCreateIsReadableAfterUnlock(t *testing.T) {
	src := newFixedSource(t)
	v, wk := create(t, src)
	sealed, err := v.Seal("cr_aaaaaaaaaaaa", []byte("sk-real-upstream-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed.Ciphertext, []byte("sk-real-upstream-secret")) {
		t.Fatal("the ciphertext holds the plaintext")
	}

	again := unlock(t, src, wk)
	if err := again.Put("cr_aaaaaaaaaaaa", sealed); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := again.Credential("cr_aaaaaaaaaaaa")
	if !ok || string(got) != "sk-real-upstream-secret" {
		t.Fatalf("Credential = %q, %v; want the sealed secret", got, ok)
	}
}

func TestACiphertextMovedToAnotherIDFailsToDecrypt(t *testing.T) {
	v, _ := create(t, newFixedSource(t))
	sealed, err := v.Seal("cr_aaaaaaaaaaaa", []byte("secret-a"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	err = v.Put("cr_bbbbbbbbbbbb", sealed)
	if !errors.Is(err, vault.ErrDecrypt) {
		t.Fatalf("Put under another id = %v, want ErrDecrypt", err)
	}
	if _, ok := v.Credential("cr_bbbbbbbbbbbb"); ok {
		t.Fatal("a ciphertext that failed to decrypt was cached")
	}
}

func TestAnotherMasterKeyDoesNotUnlock(t *testing.T) {
	_, wk := create(t, newFixedSource(t))
	_, err := vault.Unlock(newFixedSource(t), wk, vault.Options{AllowDebug: true})
	if !errors.Is(err, vault.ErrWrongMasterKey) {
		t.Fatalf("Unlock with another master key = %v, want ErrWrongMasterKey", err)
	}
}

func TestTheFingerprintNamesTheMasterKeyWithoutRevealingIt(t *testing.T) {
	src := newFixedSource(t)
	_, a := create(t, src)
	_, b := create(t, src)
	_, other := create(t, newFixedSource(t))
	if a.Fingerprint == "" || a.Fingerprint != b.Fingerprint {
		t.Errorf("one master key gave fingerprints %q and %q, want one non-empty value", a.Fingerprint, b.Fingerprint)
	}
	if a.Fingerprint == other.Fingerprint {
		t.Errorf("two master keys share the fingerprint %q", a.Fingerprint)
	}
	if a.ID == b.ID {
		t.Errorf("two data keys share the id %q", a.ID)
	}
	if strings.Contains(a.Fingerprint, base64.StdEncoding.EncodeToString(src.key)[:8]) ||
		strings.Contains(a.Fingerprint, hex.EncodeToString(src.key)[:8]) {
		t.Errorf("the fingerprint %q shows the master key", a.Fingerprint)
	}
}

func TestATamperedOrForeignCiphertextIsRefused(t *testing.T) {
	v, _ := create(t, newFixedSource(t))
	other, _ := create(t, newFixedSource(t))
	good, err := v.Seal("cr_aaaaaaaaaaaa", []byte("secret-a"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	flipped := good
	flipped.Ciphertext = bytes.Clone(good.Ciphertext)
	flipped.Ciphertext[0] ^= 1
	foreign, err := other.Seal("cr_aaaaaaaaaaaa", []byte("secret-a"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	relabelled := foreign
	relabelled.DataKeyID = v.DataKeyID()

	for name, s := range map[string]vault.Sealed{
		"a flipped bit":                 flipped,
		"sealed under another data key": foreign,
		"another data key, relabelled":  relabelled,
	} {
		if err := v.Put("cr_aaaaaaaaaaaa", s); err == nil {
			t.Errorf("Put(%s) succeeded, want an error", name)
		}
	}
	if !errors.Is(v.Put("cr_aaaaaaaaaaaa", foreign), vault.ErrUnknownDataKey) {
		t.Error("a ciphertext naming another data key is not refused as ErrUnknownDataKey")
	}
}

func allZero(b []byte) bool { return len(b) > 0 && bytes.Count(b, []byte{0}) == len(b) }

func TestTheMasterKeyIsWipedOnceTheDataKeyIsUnwrapped(t *testing.T) {
	src := newFixedSource(t)
	_, wk := create(t, src)
	unlock(t, src, wk)
	if _, err := vault.Unlock(src, vault.WrappedKey{ID: wk.ID, Fingerprint: wk.Fingerprint}, vault.Options{AllowDebug: true}); err == nil {
		t.Fatal("Unlock of a corrupt wrapped key succeeded")
	}
	if len(src.issued) != 3 {
		t.Fatalf("the source was read %d times, want 3", len(src.issued))
	}
	for i, k := range src.issued {
		if !allZero(k) {
			t.Errorf("master key copy %d was not wiped", i)
		}
	}
}

func TestEachCredentialIsCachedOnce(t *testing.T) {
	v, _ := create(t, newFixedSource(t))
	put(t, v, "cr_aaaaaaaaaaaa", "secret-a")
	a, _ := v.Credential("cr_aaaaaaaaaaaa")
	b, _ := v.Credential("cr_aaaaaaaaaaaa")
	if &a[0] != &b[0] {
		t.Fatal("Credential returned two copies of one credential")
	}
}

func TestRotationWipesThePreviousPlaintext(t *testing.T) {
	v, _ := create(t, newFixedSource(t))
	put(t, v, "cr_aaaaaaaaaaaa", "old-secret")
	old, _ := v.Credential("cr_aaaaaaaaaaaa")
	put(t, v, "cr_aaaaaaaaaaaa", "new-secret")
	if !allZero(old) {
		t.Errorf("the previous plaintext is still %q after rotation", old)
	}
	if got, _ := v.Credential("cr_aaaaaaaaaaaa"); string(got) != "new-secret" {
		t.Errorf("Credential after rotation = %q, want %q", got, "new-secret")
	}
}

func TestDeleteWipesThePlaintext(t *testing.T) {
	v, _ := create(t, newFixedSource(t))
	put(t, v, "cr_aaaaaaaaaaaa", "secret-a")
	put(t, v, "cr_bbbbbbbbbbbb", "secret-b")
	a, _ := v.Credential("cr_aaaaaaaaaaaa")
	v.Delete("cr_aaaaaaaaaaaa")
	if !allZero(a) {
		t.Errorf("the deleted plaintext is still %q", a)
	}
	if _, ok := v.Credential("cr_aaaaaaaaaaaa"); ok {
		t.Error("a deleted credential is still returned")
	}
	if got, _ := v.Credential("cr_bbbbbbbbbbbb"); string(got) != "secret-b" {
		t.Errorf("deleting one credential changed another to %q", got)
	}
	v.Delete("cr_never_there")
}

func TestCloseWipesEveryPlaintextAndRefusesMore(t *testing.T) {
	v, _ := create(t, newFixedSource(t))
	put(t, v, "cr_aaaaaaaaaaaa", "secret-a")
	a, _ := v.Credential("cr_aaaaaaaaaaaa")
	v.Close()
	if !allZero(a) {
		t.Errorf("Close left the plaintext %q", a)
	}
	if _, err := v.Seal("cr_aaaaaaaaaaaa", []byte("x")); !errors.Is(err, vault.ErrClosed) {
		t.Errorf("Seal after Close = %v, want ErrClosed", err)
	}
}

func put(t *testing.T, v *vault.Vault, id, secret string) {
	t.Helper()
	s, err := v.Seal(id, []byte(secret))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := v.Put(id, s); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func TestACorruptNonceIsAnErrorNotAPanic(t *testing.T) {
	v, _ := create(t, newFixedSource(t))
	s, err := v.Seal("cr_aaaaaaaaaaaa", []byte("secret-a"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	s.Nonce = s.Nonce[:3]
	if err := v.Put("cr_aaaaaaaaaaaa", s); !errors.Is(err, vault.ErrDecrypt) {
		t.Fatalf("Put with a short nonce = %v, want ErrDecrypt", err)
	}
}
