package meter_test

import (
	"testing"

	"github.com/kirank55/kman/internal/profiles"
)

// BenchmarkReserveSettle is the meter's share of a request: a reservation
// against a daily and a monthly cap, and its settlement.
func BenchmarkReserveSettle(b *testing.B) {
	m, _ := newMeter(b)
	a := capped("k", 1<<60, 1<<60)
	o := ok(200, profiles.Usage{Seen: true, InputTokens: 50, OutputTokens: 10})
	b.ReportAllocs()
	for b.Loop() {
		r, err := m.Reserve(t0, a)
		if err != nil {
			b.Fatal(err)
		}
		m.Settle(t0, r, o)
	}
}

func BenchmarkReserveSettleConcurrent(b *testing.B) {
	m, _ := newMeter(b)
	a := capped("k", 1<<60, 1<<60)
	o := ok(200, profiles.Usage{Seen: true, InputTokens: 50, OutputTokens: 10})
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r, err := m.Reserve(t0, a)
			if err != nil {
				b.Error(err)
				return
			}
			m.Settle(t0, r, o)
		}
	})
}
