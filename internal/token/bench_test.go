package token_test

import (
	"testing"

	"github.com/kirank55/kman/internal/token"
)

// BenchmarkParse is the gateway's per-request cost of checking a token.
func BenchmarkParse(b *testing.B) {
	for _, tc := range []struct{ name, s string }{
		{"virtual key", goldenVirtualKey},
		{"child key", goldenChildKey},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := token.Parse(tc.s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMintVirtualKey(b *testing.B) {
	b.ReportAllocs()
	id := token.NewKeyID()
	for b.Loop() {
		if _, err := token.MintVirtualKey(id); err != nil {
			b.Fatal(err)
		}
	}
}
