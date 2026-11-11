package vault

import (
	"errors"
	"fmt"
)

// ErrSameMasterKey means a rekey was given the master key already in use, so
// it would leave the old key able to open everything.
var ErrSameMasterKey = errors.New("vault: the new master key is the one already in use")

// Stored is one sealed secret with the id it is bound to.
type Stored struct {
	ID string
	Sealed
}

// Rekey replaces the master key and the data key together, offline. It
// unwraps old with the master key from from, makes a new data key with id
// newID wrapped under the master key from to, and reseals every secret under
// it, returning them in the order given. It writes nothing: the caller stores
// the result in one transaction.
//
// to is read only once from has proved itself, so a source that creates its
// key as it is read creates nothing for a rekey that was never going to work.
func Rekey(from, to KeySource, old WrappedKey, newID string, secrets []Stored, opts Options) (WrappedKey, []Stored, error) {
	before, err := Unlock(from, old, opts)
	if err != nil {
		return WrappedKey{}, nil, err
	}
	defer before.Close()
	after, wk, err := Create(to, newID, opts)
	if err != nil {
		return WrappedKey{}, nil, err
	}
	defer after.Close()
	if wk.Fingerprint == old.Fingerprint {
		return WrappedKey{}, nil, fmt.Errorf("rekey to the master key from %s: %w", to, ErrSameMasterKey)
	}
	resealed := make([]Stored, 0, len(secrets))
	for _, s := range secrets {
		sealed, err := reseal(before, after, s)
		if err != nil {
			return WrappedKey{}, nil, err
		}
		resealed = append(resealed, Stored{ID: s.ID, Sealed: sealed})
	}
	return wk, resealed, nil
}

// reseal moves one secret from one vault's data key to another's, wiping the
// plaintext as it goes.
func reseal(before, after *Vault, s Stored) (Sealed, error) {
	if s.DataKeyID != before.dekID {
		return Sealed{}, fmt.Errorf("secret %s names data key %q, the current one is %q: %w", s.ID, s.DataKeyID, before.dekID, ErrUnknownDataKey)
	}
	plain, err := open(before.aead, s.Nonce, s.Ciphertext, []byte(s.ID))
	if err != nil {
		return Sealed{}, fmt.Errorf("secret %s: %w", s.ID, ErrDecrypt)
	}
	defer clear(plain)
	return after.Seal(s.ID, plain)
}
