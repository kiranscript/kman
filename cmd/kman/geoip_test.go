package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/policy/geoip"
	"github.com/kirank55/kman/internal/policy/geoip/geoiptest"
	"github.com/kirank55/kman/internal/registry"
)

func TestGeoIPPathsAreSetInConfig(t *testing.T) {
	c, err := loadConfig(writeConfig(t, goodConfig+"geoip:\n  country_db: "+root+"/var/lib/kman/geoip/country.mmdb\n  asn_db: "+root+"/var/lib/kman/geoip/asn.mmdb\n"), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if c.GeoIP.CountryDB != root+"/var/lib/kman/geoip/country.mmdb" || c.GeoIP.ASNDB != root+"/var/lib/kman/geoip/asn.mmdb" {
		t.Errorf("GeoIP = %+v", c.GeoIP)
	}
	// And what is written reads back.
	raw, err := c.marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadConfig(writeConfig(t, string(raw)), noEnv)
	if err != nil || again.GeoIP != c.GeoIP {
		t.Errorf("round trip = %+v, %v", again.GeoIP, err)
	}
	// A config with no geoip writes none.
	plain, _ := Config{DataDir: dataDir, KeySource: keySourceFile, MasterKeyFile: keyFile, ProxyListen: defaultProxyListen, AdminListen: defaultAdminListen}.marshal()
	if strings.Contains(string(plain), "geoip") {
		t.Errorf("init's config names geoip:\n%s", plain)
	}
}

func TestLoadConfigRefusesABadGeoIPSetting(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"a relative country database": {"geoip:\n  country_db: geoip/country.mmdb\n", "geoip.country_db"},
		"a relative ASN database":     {"geoip:\n  asn_db: asn.mmdb\n", "geoip.asn_db"},
		"an unknown geoip key":        {"geoip:\n  db: " + root + "/x.mmdb\n", "db"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(writeConfig(t, goodConfig+tc.yaml), noEnv)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("loadConfig = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

func countryDB(t *testing.T) string {
	t.Helper()
	return geoiptest.Write(t, t.TempDir(), "country.mmdb", "DBIP-Country-Lite", geoiptest.Range{Network: "203.0.113.0/24", Country: "US"})
}

func TestOpenGeoIPIsNothingWithNoSetting(t *testing.T) {
	db, err := openGeoIP(Config{})
	if db != nil || err != nil {
		t.Errorf("openGeoIP = %v, %v", db, err)
	}
}

func TestOpenGeoIPOpensWhatConfigNames(t *testing.T) {
	path := countryDB(t)
	db, err := openGeoIP(Config{GeoIP: GeoIPConfig{CountryDB: path}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !db.HasCountries() {
		t.Error("no countries")
	}
}

func TestOpenGeoIPSaysWhatToDoWhenTheFileIsMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "country.mmdb")
	_, err := openGeoIP(Config{GeoIP: GeoIPConfig{CountryDB: missing}})
	if err == nil {
		t.Fatal("openGeoIP succeeded")
	}
	for _, want := range []string{missing, "kman geoip update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
}

// A country lock with nothing to check it against is refused when it is set,
// with a message that says what to do, and never stored to fail later.
func TestACountryLockIsRefusedWhereThereIsNoCountryDatabase(t *testing.T) {
	_, _, err := normalizePolicy(nil)([]byte(`{"allowed_countries":["US"]}`))
	if err == nil {
		t.Fatal("the lock was accepted")
	}
	for _, want := range []string{"allowed_countries", "country_db", "kman geoip update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
	// Network locks, and everything else a policy holds, need no database.
	for _, ok := range []string{`{}`, `{"allowed_cidrs":["10.0.0.0/8"]}`, `{"rpm_limit":5}`} {
		if _, _, err := normalizePolicy(nil)([]byte(ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	// A bad policy still says what is wrong with it.
	if _, _, err := normalizePolicy(nil)([]byte(`{"allowed_countries":["USA"]}`)); err == nil || !strings.Contains(err.Error(), "USA") {
		t.Errorf("err = %v", err)
	}
}

func TestACountryLockIsAcceptedWithACountryDatabase(t *testing.T) {
	db, err := geoip.Open(countryDB(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, got, err := normalizePolicy(db)([]byte(`{"allowed_countries":["us"]}`)); err != nil || string(got) != `{"allowed_countries":["US"]}` {
		t.Errorf("= %s, %v", got, err)
	}
	// A database of ASNs only has no countries to lock to.
	asn := geoiptest.Write(t, t.TempDir(), "asn.mmdb", "DBIP-ASN-Lite", geoiptest.Range{Network: "203.0.113.0/24", ASN: 64500})
	only, err := geoip.Open("", asn)
	if err != nil {
		t.Fatal(err)
	}
	defer only.Close()
	if _, _, err := normalizePolicy(only)([]byte(`{"allowed_countries":["US"]}`)); err == nil {
		t.Error("a country lock was accepted with no country database")
	}
}

func compiledKey(t *testing.T, id, raw string) registry.Key {
	t.Helper()
	v, n, err := policy.Normalize([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return registry.Key{ID: id, Policy: string(n), PolicyVersion: v}
}

// Keys stored with a country lock, in an install whose kman.yaml has since lost
// its database, stop the server from starting rather than run unlocked.
func TestStartupNamesTheKeysLockedToCountriesWithNoDatabase(t *testing.T) {
	reg := registry.New()
	reg.Apply(registry.Change{Generation: 1, Keys: []registry.Key{
		compiledKey(t, "keyAAAAA", `{"allowed_countries":["US"]}`),
		compiledKey(t, "keyBBBBB", `{"allowed_cidrs":["10.0.0.0/8"]}`),
		compiledKey(t, "keyCCCCC", `{"allowed_countries":["DE"],"rpm_limit":3}`),
	}})
	err := checkCountryLocks(reg.Snapshot(), nil)
	if err == nil {
		t.Fatal("startup went on")
	}
	for _, want := range []string{"keyAAAAA", "keyCCCCC", "geoip", "country_db"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "keyBBBBB") {
		t.Errorf("the error names a key with no country lock: %v", err)
	}
	db, err2 := geoip.Open(countryDB(t), "")
	if err2 != nil {
		t.Fatal(err2)
	}
	defer db.Close()
	if err := checkCountryLocks(reg.Snapshot(), db); err != nil {
		t.Errorf("with a database: %v", err)
	}
}
