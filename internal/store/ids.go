package store

import (
	"crypto/rand"
	"fmt"
	"io"
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// idLen is the number of base62 characters after an id's prefix: about 71
// bits of randomness.
const idLen = 12

// NewID returns prefix followed by 12 random base62 characters read from r,
// such as "up_3fK9xQ2mPz7a": the form of every random id the store holds.
// A nil r reads crypto/rand. Repos mint their own ids; NewID is for an id
// that is needed before its row exists, such as a data key's, which its
// wrapping binds.
func NewID(r io.Reader, prefix string) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	return newID(r, prefix)
}

// newID is NewID with the source given. Bytes are drawn by rejection so
// every character is equally likely.
func newID(r io.Reader, prefix string) (string, error) {
	out := make([]byte, 0, len(prefix)+idLen)
	out = append(out, prefix...)
	var buf [32]byte
	for len(out) < cap(out) {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return "", fmt.Errorf("store: random id: %w", err)
		}
		for _, b := range buf {
			// 248 is the largest multiple of 62 below 256.
			if b < 248 && len(out) < cap(out) {
				out = append(out, base62[b%62])
			}
		}
	}
	return string(out), nil
}
