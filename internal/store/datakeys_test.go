package store_test

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

func TestNewIDIsThePrefixAndTwelveBase62(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id, err := store.NewID(nil, "dek_")
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^dek_[0-9A-Za-z]{12}$`).MatchString(id) {
			t.Fatalf("NewID = %q, want dek_ and 12 base62", id)
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q", id)
		}
		seen[id] = true
	}
}

func TestNewIDReadsTheRandomSourceGiven(t *testing.T) {
	zeros := bytes.NewReader(make([]byte, 64))
	id, err := store.NewID(zeros, "dek_")
	if err != nil || id != "dek_000000000000" {
		t.Fatalf("NewID(zeros) = %q, %v; want dek_000000000000", id, err)
	}
	if _, err := store.NewID(strings.NewReader(""), "dek_"); err == nil {
		t.Fatal("NewID with an empty random source succeeded")
	}
}

func dataKey(id string) store.DataKey {
	return store.DataKey{
		ID: id, Wrapped: []byte("wrapped"), Nonce: []byte("nonce-12byte"),
		KEKFingerprint: "mkf_00112233445566778899aabbccddeeff", CreatedAt: now,
	}
}

func TestCurrentDataKeyReadsBackTheOneCreated(t *testing.T) {
	s := open(t)
	want := dataKey("dek_AAAAAAAAAAAA")
	do(t, s, func(tx *store.Tx) error { return tx.CreateDataKey(want) })
	var got store.DataKey
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.CurrentDataKey()
		return err
	})
	if got.ID != want.ID || !bytes.Equal(got.Wrapped, want.Wrapped) || !bytes.Equal(got.Nonce, want.Nonce) ||
		got.KEKFingerprint != want.KEKFingerprint || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CurrentDataKey = %+v, want %+v", got, want)
	}
}

func TestCurrentDataKeyOfAnEmptyStoreIsNotFound(t *testing.T) {
	s := open(t)
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.CurrentDataKey()
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CurrentDataKey = %v, want ErrNotFound", err)
	}
}

func TestCreateDataKeyRefusesASecondCurrentKey(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error { return tx.CreateDataKey(dataKey("dek_AAAAAAAAAAAA")) })
	err := s.Do(t.Context(), func(tx *store.Tx) error { return tx.CreateDataKey(dataKey("dek_BBBBBBBBBBBB")) })
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a second current data key = %v, want ErrConflict", err)
	}
}
