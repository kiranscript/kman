package store_test

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/store"
)

// withCanary opens a store holding one canary key, cafe0001: a key with no
// credential, and its canary row.
func withCanary(t *testing.T) *store.Store {
	t.Helper()
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		k := store.VirtualKey{ID: "cafe0001", Name: "decoy", Kind: store.KeyCanary, PolicyJSON: `{}`, PolicyVersion: 1, TagsJSON: `[]`, CreatedAt: now}
		if _, err := tx.CreateKey(k, hash); err != nil {
			return err
		}
		_, err := tx.CreateCanary(store.Canary{KeyID: "cafe0001", Planted: "the deploy repo's .env", IgnoreJSON: `["192.0.2.0/24"]`, ArmedAt: now})
		return err
	})
	return s
}

func TestACanaryReadsBackArmed(t *testing.T) {
	s := withCanary(t)
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.Canary("cafe0001")
		if err != nil {
			return err
		}
		if got.Planted != "the deploy repo's .env" || got.IgnoreJSON != `["192.0.2.0/24"]` || !got.ArmedAt.Equal(now) ||
			!got.FiredAt.IsZero() || !got.LastFiredAt.IsZero() || got.Fires != 0 || got.Last != (store.CanaryUse{}) {
			t.Errorf("Canary = %+v, want an armed canary that has not fired", got)
		}
		all, err := tx.Canaries()
		if err != nil || len(all) != 1 || all[0] != got {
			t.Errorf("Canaries = %+v (%v), want just the one", all, err)
		}
		return nil
	})
}

func TestACanaryNeedsACanaryKey(t *testing.T) {
	s := withCredential(t)
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.CreateKey(virtualKey("abcd1234", "app"), hash); err != nil {
			return err
		}
		if _, err := tx.CreateCanary(store.Canary{KeyID: "abcd1234", Planted: "x", IgnoreJSON: `[]`, ArmedAt: now}); err == nil {
			t.Error("CreateCanary accepted a standard key")
		}
		if _, err := tx.Canary("abcd1234"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Canary of a standard key = %v, want ErrNotFound", err)
		}
		return nil
	})
}

func TestTheFirstUseOfAnArmedCanaryFiresIt(t *testing.T) {
	s := withCanary(t)
	use := store.CanaryUse{Address: netip.MustParseAddr("203.0.113.7"), Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom", Client: "curl/8"}
	t1 := now.Add(time.Hour)
	do(t, s, func(tx *store.Tx) error {
		first, c, err := tx.RecordCanaryUse("cafe0001", t1, use)
		if err != nil {
			return err
		}
		if !first || !c.FiredAt.Equal(t1) || !c.LastFiredAt.Equal(t1) || c.Fires != 1 || c.Last != use {
			t.Errorf("first use: first=%v %+v", first, c)
		}
		// A second use while it is fired only updates what it saw.
		later := store.CanaryUse{Address: netip.MustParseAddr("2001:db8::1"), Country: "FR", ASN: 12322, ASOrg: "Free", Client: "none"}
		t2 := t1.Add(time.Minute)
		first, c, err = tx.RecordCanaryUse("cafe0001", t2, later)
		if err != nil {
			return err
		}
		if first || !c.FiredAt.Equal(t1) || !c.LastFiredAt.Equal(t2) || c.Fires != 2 || c.Last != later {
			t.Errorf("second use: first=%v %+v", first, c)
		}
		return nil
	})
}

func TestResettingACanaryArmsItAgainAndKeepsItsHistory(t *testing.T) {
	s := withCanary(t)
	t1, t2 := now.Add(time.Hour), now.Add(2*time.Hour)
	do(t, s, func(tx *store.Tx) error {
		if changed, _, err := tx.ResetCanary("cafe0001", t1); err != nil || changed {
			t.Errorf("reset of an armed canary: changed=%v err=%v, want no change", changed, err)
		}
		if _, _, err := tx.RecordCanaryUse("cafe0001", t1, store.CanaryUse{Country: "DE"}); err != nil {
			return err
		}
		changed, c, err := tx.ResetCanary("cafe0001", t2)
		if err != nil || !changed {
			t.Fatalf("reset of a fired canary: changed=%v err=%v", changed, err)
		}
		if !c.FiredAt.IsZero() || !c.ArmedAt.Equal(t2) || !c.LastFiredAt.Equal(t1) || c.Fires != 1 || c.Last.Country != "DE" {
			t.Errorf("after reset: %+v, want armed at the reset with its history kept", c)
		}
		first, _, err := tx.RecordCanaryUse("cafe0001", t2.Add(time.Second), store.CanaryUse{})
		if err != nil || !first {
			t.Errorf("a use after a reset: first=%v err=%v, want it to fire again", first, err)
		}
		return nil
	})
}

func TestACanaryUpdateChangesWhereItWasPlantedAndItsIgnoreList(t *testing.T) {
	s := withCanary(t)
	do(t, s, func(tx *store.Tx) error {
		c, err := tx.UpdateCanary("cafe0001", "the wiki", `["198.51.100.7"]`)
		if err != nil {
			return err
		}
		if c.Planted != "the wiki" || c.IgnoreJSON != `["198.51.100.7"]` {
			t.Errorf("UpdateCanary = %+v", c)
		}
		if _, err := tx.UpdateCanary("nokey000", "x", `[]`); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UpdateCanary of a missing canary = %v, want ErrNotFound", err)
		}
		return nil
	})
}

func TestACanaryIgnoresTheAddressesOnItsList(t *testing.T) {
	c := store.Canary{IgnoreJSON: `["192.0.2.0/24", "198.51.100.7", "2001:db8::/32"]`}
	for addr, want := range map[string]bool{
		"192.0.2.99":         true,
		"192.0.3.1":          false,
		"198.51.100.7":       true,
		"198.51.100.8":       false,
		"2001:db8:abcd::1":   true,
		"2001:db9::1":        false,
		"::ffff:192.0.2.5":   true, // an IPv4 address that arrived mapped is still that address
		"::ffff:203.0.113.5": false,
	} {
		if got := c.Ignored(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Ignored(%s) = %v, want %v", addr, got, want)
		}
	}
	if (store.Canary{IgnoreJSON: `[]`}).Ignored(netip.MustParseAddr("192.0.2.1")) {
		t.Error("an empty ignore list ignores an address")
	}
	if (store.Canary{IgnoreJSON: `["192.0.2.0/24"]`}).Ignored(netip.Addr{}) {
		t.Error("a request with no address (a Unix socket) is ignored: only an address can be known")
	}
	// A list that cannot be read ignores nothing: a canary fails loud.
	if (store.Canary{IgnoreJSON: `not json`}).Ignored(netip.MustParseAddr("192.0.2.1")) {
		t.Error("a broken ignore list ignores an address")
	}
}
