package geoip_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/policy/geoip"
	"github.com/kirank55/kman/internal/policy/geoip/geoiptest"
)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestLookupReadsCountryAndASNFromSeparateDatabases(t *testing.T) {
	dir := t.TempDir()
	country := geoiptest.Write(t, dir, "country.mmdb", "DBIP-Country-Lite",
		geoiptest.Range{Network: "203.0.113.0/24", Country: "us"},
		geoiptest.Range{Network: "2001:db8::/32", Country: "DE"})
	asn := geoiptest.Write(t, dir, "asn.mmdb", "DBIP-ASN-Lite",
		geoiptest.Range{Network: "203.0.113.0/24", ASN: 64500})
	db, err := geoip.Open(country, asn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	for _, c := range []struct {
		ip   string
		want geoip.Info
	}{
		{"203.0.113.9", geoip.Info{Country: "US", ASN: 64500}}, // the code is upper-cased
		{"2001:db8::1", geoip.Info{Country: "DE"}},             // an address the ASN database doesn't hold
		{"::ffff:203.0.113.9", geoip.Info{Country: "US", ASN: 64500}},
		{"198.51.100.1", geoip.Info{}}, // in neither
		{"", geoip.Info{}},             // no address, as on a Unix socket
	} {
		var a netip.Addr
		if c.ip != "" {
			a = addr(c.ip)
		}
		if got := db.Lookup(a); got != c.want {
			t.Errorf("Lookup(%q) = %+v, want %+v", c.ip, got, c.want)
		}
	}
}

func TestOneDatabaseMayHoldBoth(t *testing.T) {
	path := geoiptest.Write(t, t.TempDir(), "both.mmdb", "Both",
		geoiptest.Range{Network: "203.0.113.0/24", Country: "FR", ASN: 64501})
	db, err := geoip.Open(path, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got, want := db.Lookup(addr("203.0.113.1")), (geoip.Info{Country: "FR", ASN: 64501}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAnASNOnlyDatabaseHasNoCountries(t *testing.T) {
	asn := geoiptest.Write(t, t.TempDir(), "asn.mmdb", "ASN", geoiptest.Range{Network: "203.0.113.0/24", ASN: 64500})
	db, err := geoip.Open("", asn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.HasCountries() {
		t.Error("HasCountries is true with no country database")
	}
	if got := db.Lookup(addr("203.0.113.1")); got != (geoip.Info{ASN: 64500}) {
		t.Errorf("got %+v", got)
	}
}

func TestOpenSaysWhatIsWrong(t *testing.T) {
	dir := t.TempDir()
	notDB := filepath.Join(dir, "text.mmdb")
	if err := os.WriteFile(notDB, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ country, asn, want string }{
		"missing":  {filepath.Join(dir, "none.mmdb"), "", "none.mmdb"},
		"not mmdb": {notDB, "", "text.mmdb"},
		"asn":      {"", notDB, "text.mmdb"},
		"neither":  {"", "", "no GeoIP database"},
	} {
		t.Run(name, func(t *testing.T) {
			db, err := geoip.Open(c.country, c.asn)
			if err == nil {
				db.Close()
				t.Fatal("Open succeeded")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error %q does not name %q", err, c.want)
			}
		})
	}
}

func TestANilDatabaseKnowsNothing(t *testing.T) {
	var db *geoip.DB
	if got := db.Lookup(addr("203.0.113.1")); got != (geoip.Info{}) {
		t.Errorf("got %+v", got)
	}
	if db.HasCountries() {
		t.Error("a nil database has countries")
	}
	if err := db.Close(); err != nil {
		t.Error(err)
	}
}
