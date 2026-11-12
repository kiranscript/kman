// Package geoip finds the country and the autonomous system (ASN) an address
// belongs to, from MaxMind DB (.mmdb) files on disk: DB-IP Lite, which kman
// geoip update downloads, or any database the operator supplies.
//
// It only reads files. It links no network code, and a test holds it to that
// (ADR 0008): the download belongs to the kman geoip update command alone.
package geoip

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Info is what is known of an address. A field is zero when no database says.
type Info struct {
	// Country is an ISO 3166-1 alpha-2 code in capitals, such as "US".
	Country string
	// ASN is the autonomous system number, such as 15169.
	ASN uint32
}

// record is the part of a database record kman reads, in the shape MaxMind and
// DB-IP both use.
type record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	ASN uint32 `maxminddb:"autonomous_system_number"`
}

// DB answers lookups from up to two databases, one for countries and one for
// ASNs, which DB-IP publishes as separate files. A database that holds both
// may be given for both. A nil *DB knows nothing.
type DB struct {
	country, asn *maxminddb.Reader
}

// Open opens a country database and an ASN database. Either path may be empty,
// but not both.
func Open(countryPath, asnPath string) (*DB, error) {
	if countryPath == "" && asnPath == "" {
		return nil, errors.New("no GeoIP database is given")
	}
	db := &DB{}
	var err error
	if countryPath != "" {
		if db.country, err = open(countryPath); err != nil {
			return nil, err
		}
	}
	switch {
	case asnPath == "":
	case asnPath == countryPath:
		db.asn = db.country
	default:
		if db.asn, err = open(asnPath); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func open(path string) (*maxminddb.Reader, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s is not a GeoIP database kman can read: %w", path, err)
	}
	return r, nil
}

// HasCountries reports whether the database was given a country database, which
// a country lock needs.
func (d *DB) HasCountries() bool { return d != nil && d.country != nil }

// Lookup returns what the databases say of a. The zero Info is an address they
// don't hold, and an invalid address.
func (d *DB) Lookup(a netip.Addr) Info {
	if d == nil || !a.IsValid() {
		return Info{}
	}
	a = a.Unmap()
	var info Info
	if d.country != nil {
		var rec record
		if err := d.country.Lookup(a).Decode(&rec); err == nil {
			info.Country = strings.ToUpper(rec.Country.ISOCode)
			if d.asn == d.country {
				info.ASN = rec.ASN
			}
		}
	}
	if d.asn != nil && d.asn != d.country {
		var rec record
		if err := d.asn.Lookup(a).Decode(&rec); err == nil {
			info.ASN = rec.ASN
		}
	}
	return info
}

// Close releases the files. It is safe on a nil DB.
func (d *DB) Close() error {
	if d == nil {
		return nil
	}
	var err error
	if d.country != nil {
		err = d.country.Close()
	}
	if d.asn != nil && d.asn != d.country {
		err = errors.Join(err, d.asn.Close())
	}
	return err
}
