package geoiptest_test

import (
	"os"
	"testing"

	"github.com/kirank55/kman/internal/policy/geoip/geoiptest"
)

func TestWriteMakesADatabaseFile(t *testing.T) {
	path := geoiptest.Write(t, t.TempDir(), "t.mmdb", "Test-DB", geoiptest.Range{Network: "203.0.113.0/24", Country: "US", ASN: 64500})
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("no database written: %v", err)
	}
}
