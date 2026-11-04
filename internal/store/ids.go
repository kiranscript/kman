package store

import (
	"fmt"
	"io"
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// idLen is the number of base62 characters after an id's prefix: about 71
// bits of randomness.
const idLen = 12

// newID returns prefix followed by idLen random base62 characters read from r,
// such as "up_3fK9xQ2mPz7a". Bytes are drawn by rejection so every character
// is equally likely.
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
