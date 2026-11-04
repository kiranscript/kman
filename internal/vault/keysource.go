package vault

import (
	"crypto/rand"
)

// KeySource is where kman obtains the master key at startup (ADR 0003).
type KeySource interface {
	// MasterKey returns the master key in a fresh buffer that the caller
	// owns and wipes.
	MasterKey() ([]byte, error)
	// String names the source for errors and logs. It never holds the key.
	String() string
}

// NewMasterKey returns a fresh random master key. The caller wipes it.
func NewMasterKey() []byte {
	mk := make([]byte, KeySize)
	rand.Read(mk)
	return mk
}
