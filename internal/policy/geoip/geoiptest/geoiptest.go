// Package geoiptest writes small MaxMind DB (.mmdb) files for tests, in the
// shape DB-IP Lite and MaxMind publish. Only test files import it, so the
// writer never reaches the kman binary.
package geoiptest

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

// Range is one network of a test database. A zero Country or ASN leaves that
// field out of the record, as a database that holds only the other would.
type Range struct {
	Network string // CIDR, such as "203.0.113.0/24"
	Country string // ISO 3166-1 alpha-2 code
	ASN     uint32
}

// Write writes a database holding ranges to dir and returns its path. kind is
// the database type recorded in its metadata, such as "DBIP-Country-Lite".
func Write(tb testing.TB, dir, name, kind string, ranges ...Range) string {
	tb.Helper()
	w, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: kind, RecordSize: 28, IncludeReservedNetworks: true})
	if err != nil {
		tb.Fatal(err)
	}
	for _, r := range ranges {
		prefix, err := netip.ParsePrefix(r.Network)
		if err != nil {
			tb.Fatal(err)
		}
		_, network, err := net.ParseCIDR(prefix.String())
		if err != nil {
			tb.Fatal(err)
		}
		rec := mmdbtype.Map{}
		if r.Country != "" {
			rec["country"] = mmdbtype.Map{"iso_code": mmdbtype.String(r.Country)}
		}
		if r.ASN != 0 {
			rec["autonomous_system_number"] = mmdbtype.Uint32(r.ASN)
			rec["autonomous_system_organization"] = mmdbtype.String("Test Network")
		}
		if err := w.Insert(network, rec); err != nil {
			tb.Fatal(err)
		}
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := w.WriteTo(f); err != nil {
		tb.Fatal(err)
	}
	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
	return path
}
