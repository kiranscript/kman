package token

import (
	"crypto/rand"
	"io"
)

// alphabet is base62 in the order of the scanner pattern's [0-9A-Za-z].
const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// digit returns the value of a base62 character, or -1.
func digit(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'A' <= c && c <= 'Z':
		return int(c-'A') + 10
	case 'a' <= c && c <= 'z':
		return int(c-'a') + 36
	}
	return -1
}

// decode returns the value of a short base62 string. s must be base62 and
// short enough not to overflow a uint64 (11 characters or fewer).
func decode(s string) uint64 {
	var n uint64
	for i := 0; i < len(s); i++ {
		n = n*62 + uint64(digit(s[i]))
	}
	return n
}

// encode writes the big-endian number in num to dst in base62, most
// significant digit first, padded with leading zeros to fill dst. dst must be
// long enough for the number.
func encode(dst, num []byte) {
	n := make([]byte, len(num))
	copy(n, num)
	for i := len(dst) - 1; i >= 0; i-- {
		// Divide n by 62 in place; the remainder is the next digit.
		var rem uint
		for j := range n {
			cur := rem<<8 | uint(n[j])
			n[j] = byte(cur / 62)
			rem = cur % 62
		}
		dst[i] = alphabet[rem]
	}
}

// encodeUint writes n to dst in base62, padded with leading zeros.
func encodeUint(dst []byte, n uint64) {
	for i := len(dst) - 1; i >= 0; i-- {
		dst[i] = alphabet[n%62]
		n /= 62
	}
}

// randomBase62 fills dst with uniformly random base62 characters.
func randomBase62(dst []byte) {
	readBase62(rand.Reader, dst) // never fails (crypto/rand, Go 1.24+)
}

// readBase62 fills dst with uniformly random base62 characters drawn from r.
func readBase62(r io.Reader, dst []byte) error {
	var buf [64]byte
	for i := 0; i < len(dst); {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return err
		}
		for _, c := range buf {
			// 248 is the largest multiple of 62 that fits a byte, so
			// rejecting bytes at or above it keeps every digit equally likely.
			if c < 248 && i < len(dst) {
				dst[i] = alphabet[c%62]
				i++
			}
		}
	}
	return nil
}
