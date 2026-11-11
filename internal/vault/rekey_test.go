package vault_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/vault"
)

const newDEKID = "dek_BBBBBBBBBBBB"

var debug = vault.Options{AllowDebug: true}

// stored seals each named secret under v, as the store would hold them.
func stored(t *testing.T, v *vault.Vault, secrets map[string]string) []vault.Stored {
	t.Helper()
	var out []vault.Stored
	for id, plain := range secrets {
		s, err := v.Seal(id, []byte(plain))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, vault.Stored{ID: id, Sealed: s})
	}
	return out
}

func TestRekeyedSecretsOpenUnderTheNewMasterKeyAndNotTheOld(t *testing.T) {
	from, to := newFixedSource(t), newFixedSource(t)
	v, oldKey := create(t, from)
	secrets := map[string]string{"cr_one": "sk-one", "ch_two": "https://hooks.example/two"}

	newKey, resealed, err := vault.Rekey(from, to, oldKey, newDEKID, stored(t, v, secrets), debug)
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if newKey.ID != newDEKID {
		t.Errorf("new data key id = %q, want %q", newKey.ID, newDEKID)
	}
	if len(resealed) != len(secrets) {
		t.Fatalf("Rekey returned %d secrets, want %d", len(resealed), len(secrets))
	}

	unlocked := unlock(t, to, newKey)
	for _, s := range resealed {
		if s.DataKeyID != newDEKID {
			t.Errorf("%s names data key %q, want %q", s.ID, s.DataKeyID, newDEKID)
		}
		if err := unlocked.Put(s.ID, s.Sealed); err != nil {
			t.Fatalf("Put %s: %v", s.ID, err)
		}
		if got, _ := unlocked.Credential(s.ID); string(got) != secrets[s.ID] {
			t.Errorf("%s = %q, want %q", s.ID, got, secrets[s.ID])
		}
	}
	if _, err := vault.Unlock(from, newKey, debug); !errors.Is(err, vault.ErrWrongMasterKey) {
		t.Errorf("the old master key on the new data key: %v, want ErrWrongMasterKey", err)
	}
}

func TestRekeyMakesANewDataKeyInsteadOfRewrappingTheOld(t *testing.T) {
	from, to := newFixedSource(t), newFixedSource(t)
	v, oldKey := create(t, from)
	newKey, resealed, err := vault.Rekey(from, to, oldKey, newDEKID, stored(t, v, map[string]string{"cr_one": "sk-one"}), debug)
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	// The old vault holds the old data key: it must not open what Rekey sealed.
	foreign := resealed[0].Sealed
	foreign.DataKeyID = v.DataKeyID()
	if err := v.Put("cr_one", foreign); !errors.Is(err, vault.ErrDecrypt) {
		t.Errorf("the old data key on a resealed secret: %v, want ErrDecrypt", err)
	}
	if newKey.Fingerprint == oldKey.Fingerprint {
		t.Error("the new data key names the old master key")
	}
}

func TestRekeyRefusesTheMasterKeyAlreadyInUse(t *testing.T) {
	from := newFixedSource(t)
	v, oldKey := create(t, from)
	_, _, err := vault.Rekey(from, from, oldKey, newDEKID, stored(t, v, map[string]string{"cr_one": "sk-one"}), debug)
	if !errors.Is(err, vault.ErrSameMasterKey) {
		t.Errorf("Rekey to the same master key: %v, want ErrSameMasterKey", err)
	}
}

func TestRekeyRefusesAnOldMasterKeyThatDoesNotUnwrap(t *testing.T) {
	v, oldKey := create(t, newFixedSource(t))
	to := newFixedSource(t)
	_, _, err := vault.Rekey(newFixedSource(t), to, oldKey, newDEKID, stored(t, v, nil), debug)
	if !errors.Is(err, vault.ErrWrongMasterKey) {
		t.Errorf("Rekey from the wrong master key: %v, want ErrWrongMasterKey", err)
	}
	if to.reads != 0 {
		t.Error("Rekey read the new master key before the old one had proved itself, so a new key file could be written for nothing")
	}
}

func TestRekeyNamesTheSecretThatDoesNotDecrypt(t *testing.T) {
	from := newFixedSource(t)
	v, oldKey := create(t, from)
	secrets := stored(t, v, map[string]string{"cr_bad": "sk-bad"})
	secrets[0].Ciphertext[0] ^= 1
	_, _, err := vault.Rekey(from, newFixedSource(t), oldKey, newDEKID, secrets, debug)
	if !errors.Is(err, vault.ErrDecrypt) {
		t.Fatalf("Rekey with a tampered secret: %v, want ErrDecrypt", err)
	}
	if !strings.Contains(err.Error(), "cr_bad") {
		t.Errorf("the error does not name the secret: %v", err)
	}
}

func TestRekeyRefusesASecretUnderAnotherDataKey(t *testing.T) {
	from := newFixedSource(t)
	v, oldKey := create(t, from)
	secrets := stored(t, v, map[string]string{"cr_other": "sk-other"})
	secrets[0].DataKeyID = "dek_CCCCCCCCCCCC"
	_, _, err := vault.Rekey(from, newFixedSource(t), oldKey, newDEKID, secrets, debug)
	if !errors.Is(err, vault.ErrUnknownDataKey) {
		t.Errorf("Rekey with a secret under another data key: %v, want ErrUnknownDataKey", err)
	}
}

func TestRekeyWipesBothMasterKeys(t *testing.T) {
	from, to := newFixedSource(t), newFixedSource(t)
	v, oldKey := create(t, from)
	if _, _, err := vault.Rekey(from, to, oldKey, newDEKID, stored(t, v, map[string]string{"cr_one": "sk-one"}), debug); err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	for _, src := range []*fixedSource{from, to} {
		for _, b := range src.issued {
			for _, c := range b {
				if c != 0 {
					t.Fatal("a master key buffer was not wiped")
				}
			}
		}
	}
}
