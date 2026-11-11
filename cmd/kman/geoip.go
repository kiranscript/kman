package main

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/policy/geoip"
	"github.com/kirank55/kman/internal/registry"
)

// GeoIPConfig is the geoip section of kman.yaml: where the GeoIP databases are,
// as absolute paths to MaxMind DB (.mmdb) files. DB-IP Lite publishes country
// and ASN as two files, which kman geoip update downloads; a database an
// operator supplies may hold both, and may be named for both. With neither set
// kman has no GeoIP: a key can't be locked to countries, and events carry no
// country or ASN.
type GeoIPConfig struct {
	// CountryDB is the database of countries, which country locks need.
	CountryDB string `yaml:"country_db,omitempty"`
	// ASNDB is the database of autonomous system numbers.
	ASNDB string `yaml:"asn_db,omitempty"`
}

func (g GeoIPConfig) enabled() bool { return g.CountryDB != "" || g.ASNDB != "" }

func (g GeoIPConfig) check() error {
	for _, p := range []struct{ key, path string }{{"geoip.country_db", g.CountryDB}, {"geoip.asn_db", g.ASNDB}} {
		if p.path != "" && !filepath.IsAbs(p.path) {
			return fmt.Errorf("%s %q is not an absolute path", p.key, p.path)
		}
	}
	return nil
}

// openGeoIP opens the databases of kman.yaml, and returns nil when it names
// none. A named database that can't be opened is an error: kman never starts
// as though a GeoIP lock were checked when it can't be.
func openGeoIP(cfg Config) (*geoip.DB, error) {
	if !cfg.GeoIP.enabled() {
		return nil, nil
	}
	db, err := geoip.Open(cfg.GeoIP.CountryDB, cfg.GeoIP.ASNDB)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("geoip: %w: run kman geoip update to download the databases, or supply your own .mmdb files", err)
		}
		return nil, fmt.Errorf("geoip: %w", err)
	}
	return db, nil
}

// errNoCountryDB is what a country lock is refused with when kman has no country database.
const errNoCountryDB = "allowed_countries needs a GeoIP country database, and this kman has none: " +
	"run kman geoip update, set geoip.country_db in kman.yaml to the file it wrote, restart kman, and set the policy again"

// normalizePolicy is policy.Normalize, and also refuses a country lock when db
// holds no countries to check it against: such a lock is refused where it is
// set, with what to do, and never stored to fail a request later.
func normalizePolicy(db *geoip.DB) func(raw []byte) (int, []byte, error) {
	return func(raw []byte) (int, []byte, error) {
		version, normalized, err := policy.Normalize(raw)
		if err != nil {
			return 0, nil, err
		}
		p, err := policy.Parse(string(normalized))
		if err != nil {
			return 0, nil, err
		}
		if len(p.AllowedCountries) > 0 && !db.HasCountries() {
			return 0, nil, errors.New("the policy: " + errNoCountryDB)
		}
		return version, normalized, nil
	}
}

// checkCountryLocks refuses to start kman with keys locked to countries and no
// country database, as when geoip was taken out of kman.yaml after the locks
// were set. The gateway would refuse those keys' requests; stopping says why
// before anyone sees a key fail.
func checkCountryLocks(snap *registry.Snapshot, db *geoip.DB) error {
	if db.HasCountries() {
		return nil
	}
	var ids []string
	for k := range snap.Keys() {
		if k.Compiled.CountryLocked() {
			ids = append(ids, k.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return fmt.Errorf("these keys are locked to countries, and kman.yaml sets no geoip country_db to check them against: %s. "+
		"Run kman geoip update and set geoip.country_db, or remove the lock with kman key update", strings.Join(ids, ", "))
}
