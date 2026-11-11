package store_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

const retention = 30 * 24 * time.Hour

func hashOf(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// secretsOf returns a key's secrets by hash byte, to read states at a glance.
func secretsOf(t *testing.T, s *store.Store, id string) map[byte]store.KeySecret {
	t.Helper()
	out := map[byte]store.KeySecret{}
	do(t, s, func(tx *store.Tx) error {
		secrets, err := tx.KeySecrets(id)
		for _, k := range secrets {
			out[k.Hash[0]] = k
		}
		return err
	})
	return out
}

func withKey(t *testing.T) *store.Store {
	t.Helper()
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateKey(virtualKey("abcd1234", "my-app"), hash)
		return err
	})
	return s
}

func TestRotatingWithAGraceKeepsTheOldSecretAsPreviousUntilTheGraceEnds(t *testing.T) {
	s := withKey(t) // current secret: hash, all 7s
	at := now.Add(time.Minute)
	do(t, s, func(tx *store.Tx) error {
		return tx.RotateKeySecret("abcd1234", hashOf(8), at, time.Hour, retention)
	})
	got := secretsOf(t, s, "abcd1234")
	if len(got) != 2 {
		t.Fatalf("secrets = %+v, want the new current and the old previous", got)
	}
	if got[8].State != store.SecretCurrent || !got[8].ValidUntil.IsZero() || !got[8].CreatedAt.Equal(at) {
		t.Errorf("new secret = %+v", got[8])
	}
	if got[7].State != store.SecretPrevious || !got[7].ValidUntil.Equal(at.Add(time.Hour)) {
		t.Errorf("old secret = %+v, want previous until %v", got[7], at.Add(time.Hour))
	}
}

func TestRotatingWithNoGraceRetiresTheOldSecretAtOnce(t *testing.T) {
	s := withKey(t)
	at := now.Add(time.Minute)
	do(t, s, func(tx *store.Tx) error {
		return tx.RotateKeySecret("abcd1234", hashOf(8), at, 0, retention)
	})
	got := secretsOf(t, s, "abcd1234")
	if got[7].State != store.SecretRetired || !got[7].ForgetAt.Equal(at.Add(retention)) {
		t.Errorf("old secret = %+v, want retired and forgotten at %v", got[7], at.Add(retention))
	}
	if got[8].State != store.SecretCurrent {
		t.Errorf("new secret = %+v", got[8])
	}
}

func TestASecondRotationInsideTheGraceRetiresTheOlderSecretAtOnce(t *testing.T) {
	s := withKey(t)
	t1, t2 := now.Add(time.Minute), now.Add(2*time.Minute)
	do(t, s, func(tx *store.Tx) error { return tx.RotateKeySecret("abcd1234", hashOf(8), t1, time.Hour, retention) })
	do(t, s, func(tx *store.Tx) error { return tx.RotateKeySecret("abcd1234", hashOf(9), t2, time.Hour, retention) })
	got := secretsOf(t, s, "abcd1234")
	if got[7].State != store.SecretRetired || !got[7].ForgetAt.Equal(t2.Add(retention)) {
		t.Errorf("oldest = %+v, want retired at the second rotation", got[7])
	}
	if got[8].State != store.SecretPrevious || !got[8].ValidUntil.Equal(t2.Add(time.Hour)) {
		t.Errorf("middle = %+v, want previous with a fresh grace", got[8])
	}
	if got[9].State != store.SecretCurrent {
		t.Errorf("newest = %+v", got[9])
	}
}

func TestAFailedRotationLeavesTheSecretsExactlyAsTheyWere(t *testing.T) {
	s := withKey(t)
	do(t, s, func(tx *store.Tx) error {
		return tx.RotateKeySecret("abcd1234", hashOf(8), now.Add(time.Minute), time.Hour, retention)
	})
	before := secretsOf(t, s, "abcd1234")
	// The new hash is the one already current: the insert fails after the
	// states have moved, and the whole rotation must roll back.
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		return tx.RotateKeySecret("abcd1234", hashOf(8), now.Add(2*time.Minute), time.Hour, retention)
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("RotateKeySecret = %v, want ErrConflict", err)
	}
	after := secretsOf(t, s, "abcd1234")
	if len(after) != len(before) {
		t.Fatalf("secrets = %+v, want %+v", after, before)
	}
	for h, b := range before {
		if a := after[h]; a.State != b.State || !a.ValidUntil.Equal(b.ValidUntil) || !a.ForgetAt.Equal(b.ForgetAt) {
			t.Errorf("secret %d = %+v, want %+v", h, a, b)
		}
	}
}

func TestRotatingAKeyThatIsNotThereIsNotFound(t *testing.T) {
	s := withKey(t)
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		return tx.RotateKeySecret("nokey123", hashOf(8), now, 0, retention)
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RotateKeySecret = %v, want ErrNotFound", err)
	}
}

func TestSettlingRetiresAPreviousSecretWhoseGraceHasEnded(t *testing.T) {
	s := withKey(t)
	at := now.Add(time.Minute)
	do(t, s, func(tx *store.Tx) error { return tx.RotateKeySecret("abcd1234", hashOf(8), at, time.Hour, retention) })
	end := at.Add(time.Hour)

	var next time.Time
	do(t, s, func(tx *store.Tx) (err error) {
		next, err = tx.SettleKeySecrets(end.Add(-time.Second), retention)
		return err
	})
	if !next.Equal(end) {
		t.Errorf("next = %v, want the end of the grace, %v", next, end)
	}
	if got := secretsOf(t, s, "abcd1234"); got[7].State != store.SecretPrevious {
		t.Errorf("before the grace ends: %+v", got[7])
	}

	do(t, s, func(tx *store.Tx) (err error) {
		next, err = tx.SettleKeySecrets(end, retention)
		return err
	})
	got := secretsOf(t, s, "abcd1234")
	if got[7].State != store.SecretRetired || !got[7].ForgetAt.Equal(end.Add(retention)) {
		t.Errorf("after the grace: %+v, want retired and forgotten at %v", got[7], end.Add(retention))
	}
	if !next.IsZero() {
		t.Errorf("next = %v, want none: nothing is left in a grace", next)
	}
	if got[8].State != store.SecretCurrent {
		t.Errorf("the current secret changed: %+v", got[8])
	}
}

func TestSettlingForgetsARetiredSecretAfterThirtyDays(t *testing.T) {
	s := withKey(t)
	at := now.Add(time.Minute)
	do(t, s, func(tx *store.Tx) error { return tx.RotateKeySecret("abcd1234", hashOf(8), at, 0, retention) })
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.SettleKeySecrets(at.Add(retention).Add(-time.Millisecond), retention)
		return err
	})
	if got := secretsOf(t, s, "abcd1234"); got[7].State != store.SecretRetired {
		t.Fatalf("a day short of 30 days, the retired secret is %+v", got[7])
	}
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.SettleKeySecrets(at.Add(retention), retention)
		return err
	})
	got := secretsOf(t, s, "abcd1234")
	if _, ok := got[7]; ok || got[8].State != store.SecretCurrent {
		t.Errorf("after 30 days: %+v, want the retired secret forgotten and the current kept", got)
	}
}
