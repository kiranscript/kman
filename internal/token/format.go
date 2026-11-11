package token

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"hash/crc32"
	"strings"
)

// The parts of a token, fixed by ADR 0001.
const (
	virtualPrefix = "kmn_"
	childPrefix   = "ek_kmn_"
	adminPrefix   = "kma_"

	idLen     = 8
	secretLen = 43 // 256 bits in base62
	crcLen    = 6  // a CRC32 in base62
)

// IsKeyShaped reports whether s starts as a virtual key or a child key does,
// before any checksum or length is looked at: the gateway's way of finding a
// key among an app's headers, and of keeping it from the upstream. Parse is what
// decides whether it is one.
func IsKeyShaped(s string) bool {
	return strings.HasPrefix(s, virtualPrefix) || strings.HasPrefix(s, childPrefix)
}

// Hash is the SHA-256 of a token's secret: all of a secret kman stores.
// Compare hashes with Equal, not ==, so the comparison takes constant time.
type Hash [sha256.Size]byte

// Equal reports whether h and o are the same hash, in constant time.
func (h Hash) Equal(o Hash) bool {
	return subtle.ConstantTimeCompare(h[:], o[:]) == 1
}

// Token is a token parsed and checked offline. It holds the secret's hash,
// never the secret.
type Token struct {
	Kind Kind
	ID   KeyID // empty for an admin token
	Hash Hash
}

// The errors Parse returns. Neither repeats the token.
var (
	// ErrMalformed means a string is not shaped like any kman token.
	ErrMalformed = errors.New("token: malformed")
	// ErrChecksum means a string is shaped like a token but its checksum
	// does not match: it was mistyped, cut, or had a prefix added or removed.
	ErrChecksum = errors.New("token: bad checksum")
)

// Parse checks a token's shape and returns its kind, key id and secret hash,
// before any lookup.
func Parse(s string) (Token, error) {
	var t Token
	var rest string
	switch {
	case strings.HasPrefix(s, childPrefix):
		t.Kind, rest = ChildKey, s[len(childPrefix):]
	case strings.HasPrefix(s, virtualPrefix):
		t.Kind, rest = VirtualKey, s[len(virtualPrefix):]
	case strings.HasPrefix(s, adminPrefix):
		t.Kind, rest = AdminToken, s[len(adminPrefix):]
	default:
		return Token{}, ErrMalformed
	}
	if t.Kind != AdminToken {
		if len(rest) < idLen+1 || rest[idLen] != '_' || !isBase62(rest[:idLen]) {
			return Token{}, ErrMalformed
		}
		t.ID, rest = KeyID(rest[:idLen]), rest[idLen+1:]
	}
	if len(rest) != secretLen+crcLen || !isBase62(rest) {
		return Token{}, ErrMalformed
	}
	body := s[:len(s)-crcLen]
	if decode(s[len(body):]) != uint64(checksum(body)) {
		return Token{}, ErrChecksum
	}
	t.Hash = hashSecret(rest[:secretLen])
	return t, nil
}

// maxBody is the longest token before its checksum: a child key.
const maxBody = len(childPrefix) + idLen + 1 + secretLen

// checksum is the CRC32 (IEEE) of everything in a token before the checksum.
// It walks the string with the table itself because crc32.ChecksumIEEE would
// need a []byte that escapes, an allocation on every request.
func checksum(body string) uint32 {
	crc := ^uint32(0)
	for i := 0; i < len(body); i++ {
		crc = crc32.IEEETable[byte(crc)^body[i]] ^ crc>>8
	}
	return ^crc
}

func hashSecret(secret string) Hash {
	var b [secretLen]byte
	copy(b[:], secret)
	return sha256.Sum256(b[:])
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		if digit(s[i]) < 0 {
			return false
		}
	}
	return true
}
