package vault_test

import (
	"errors"
	"testing"

	"github.com/kirank55/kman/internal/vault"
)

// orderSource records whether the process was hardened when it was read.
type orderSource struct {
	*fixedSource
	hardened *bool
	sawHard  []bool
}

func (s *orderSource) MasterKey() ([]byte, error) {
	s.sawHard = append(s.sawHard, *s.hardened)
	return s.fixedSource.MasterKey()
}

func TestTheProcessIsHardenedBeforeTheMasterKeyIsRead(t *testing.T) {
	hardened := false
	vault.SetHarden(t, func() error { hardened = true; return nil })
	src := &orderSource{fixedSource: newFixedSource(t), hardened: &hardened}

	v, wk, err := vault.Create(src, vault.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v.Close()
	hardened = false
	v, err = vault.Unlock(src, wk, vault.Options{})
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	v.Close()
	if len(src.sawHard) != 2 || !src.sawHard[0] || !src.sawHard[1] {
		t.Fatalf("hardened when the key was read: %v, want [true true]", src.sawHard)
	}
}

func TestAllowDebugSkipsHardening(t *testing.T) {
	calls := 0
	vault.SetHarden(t, func() error { calls++; return nil })
	src := newFixedSource(t)
	v, wk := create(t, src) // create and unlock pass AllowDebug
	unlock(t, src, wk)
	v.Close()
	if calls != 0 {
		t.Fatalf("hardening ran %d times with AllowDebug, want 0", calls)
	}
}

func TestFailedHardeningStopsBeforeAnySecretIsRead(t *testing.T) {
	boom := errors.New("prctl refused")
	vault.SetHarden(t, func() error { return boom })
	src := newFixedSource(t)
	if _, _, err := vault.Create(src, vault.Options{}); !errors.Is(err, boom) {
		t.Fatalf("Create = %v, want the hardening error", err)
	}
	if _, err := vault.Unlock(src, vault.WrappedKey{}, vault.Options{}); !errors.Is(err, boom) {
		t.Fatalf("Unlock = %v, want the hardening error", err)
	}
	if src.reads != 0 {
		t.Fatalf("the key source was read %d times after hardening failed", src.reads)
	}
}
