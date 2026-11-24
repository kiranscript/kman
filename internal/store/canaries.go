package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"
)

// Canary is what a canary key has beyond its virtual_keys row (#87). The key
// has no credential, so it forwards nothing and any use of it is a leak.
type Canary struct {
	KeyID string
	// Planted is where the operator put it, in the operator's words.
	Planted string
	// IgnoreJSON is a JSON array of addresses and CIDR prefixes whose use does
	// not fire the canary, such as the operator's own scanners.
	IgnoreJSON string
	// ArmedAt is when the canary was made or last reset.
	ArmedAt time.Time
	// FiredAt is the first use since it was armed: zero while it is armed.
	FiredAt time.Time
	// LastFiredAt is the last use, and Fires how many uses were counted. A reset
	// keeps both.
	LastFiredAt time.Time
	Fires       int64
	// Last is what the last use looked like.
	Last CanaryUse
}

// CanaryUse is where a canary's use came from. The zero value says nothing is
// known: an address is the zero Addr for a Unix socket, and the rest are empty
// or zero until GeoIP names them.
type CanaryUse struct {
	Address netip.Addr
	Country string
	ASN     int64
	ASOrg   string
	Client  string
}

const canaryColumns = `key_id, planted, ignore_json, armed_at, fired_at, last_fired_at, fires,
	last_ip, last_country, last_asn, last_as_org, last_client`

// Ignored reports whether addr is on the canary's ignore list. A request with no
// address is never ignored, since only an address can be known, and a list that
// cannot be read ignores nothing: a canary fails loud. An IPv4 address that
// arrived mapped into IPv6 is that IPv4 address.
func (c Canary) Ignored(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	var list []string
	if err := json.Unmarshal([]byte(c.IgnoreJSON), &list); err != nil {
		return false
	}
	for _, entry := range list {
		if p, err := netip.ParsePrefix(entry); err == nil {
			if p.Contains(addr) {
				return true
			}
		} else if a, err := netip.ParseAddr(entry); err == nil && a.Unmap() == addr {
			return true
		}
	}
	return false
}

// CreateCanary adds the canary row for a key whose kind is canary. Any other key
// is ErrConflict, and a key that does not exist is ErrNotFound. The key has
// at most one.
func (tx *Tx) CreateCanary(c Canary) (Canary, error) {
	k, err := tx.Key(c.KeyID)
	if err != nil {
		return Canary{}, err
	}
	if k.Kind != KeyCanary {
		return Canary{}, fmt.Errorf("store: create canary %s: the key is not a canary: %w", c.KeyID, ErrConflict)
	}
	if c.IgnoreJSON == "" {
		c.IgnoreJSON = `[]`
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO canaries (key_id, planted, ignore_json, armed_at) VALUES (?, ?, ?, ?) RETURNING `+canaryColumns,
		c.KeyID, c.Planted, c.IgnoreJSON, toMilli(c.ArmedAt))
	return scanCanary(row, "create canary "+c.KeyID)
}

// Canary returns the canary row of a key, or ErrNotFound for a key that has
// none, a standard key included.
func (tx *Tx) Canary(keyID string) (Canary, error) {
	row := tx.tx.QueryRowContext(tx.ctx, `SELECT `+canaryColumns+` FROM canaries WHERE key_id = ?`, keyID)
	return scanCanary(row, "canary "+keyID)
}

// Canaries returns every canary, oldest key first.
func (tx *Tx) Canaries() ([]Canary, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT `+canaryColumns+` FROM canaries
		JOIN virtual_keys USING (key_id) ORDER BY virtual_keys.created_at, key_id`)
	if err != nil {
		return nil, typed(err, "list canaries")
	}
	defer rows.Close()
	var out []Canary
	for rows.Next() {
		c, err := scanCanary(rows, "list canaries")
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list canaries")
	}
	return out, nil
}

// UpdateCanary sets where a canary was planted and its ignore list.
func (tx *Tx) UpdateCanary(keyID, planted, ignoreJSON string) (Canary, error) {
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE canaries SET planted = ?, ignore_json = ? WHERE key_id = ? RETURNING `+canaryColumns,
		planted, ignoreJSON, keyID)
	return scanCanary(row, "update canary "+keyID)
}

// RecordCanaryUse counts one use of a canary at time at, and keeps what it
// looked like. first says whether it fired the canary: it was armed, so this is
// the first use since. A missing canary is ErrNotFound.
func (tx *Tx) RecordCanaryUse(keyID string, at time.Time, use CanaryUse) (first bool, c Canary, err error) {
	before, err := tx.Canary(keyID)
	if err != nil {
		return false, Canary{}, err
	}
	first = before.FiredAt.IsZero()
	var addr sql.NullString
	if use.Address.IsValid() {
		addr = sql.NullString{String: use.Address.Unmap().String(), Valid: true}
	}
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE canaries SET fired_at = COALESCE(fired_at, ?1), last_fired_at = ?1, fires = fires + 1,
			last_ip = ?2, last_country = ?3, last_asn = ?4, last_as_org = ?5, last_client = ?6
		WHERE key_id = ?7 RETURNING `+canaryColumns,
		toMilli(at), addr, nullString(use.Country), nullInt(use.ASN), nullString(use.ASOrg), nullString(use.Client), keyID)
	c, err = scanCanary(row, "record use of canary "+keyID)
	return first, c, err
}

// ResetCanary arms a fired canary again at time at, so its next use fires it.
// Its history stays: when it last fired, how many uses were counted, and what
// the last one looked like. changed is false for a canary that is already armed.
func (tx *Tx) ResetCanary(keyID string, at time.Time) (changed bool, c Canary, err error) {
	before, err := tx.Canary(keyID)
	if err != nil {
		return false, Canary{}, err
	}
	if before.FiredAt.IsZero() {
		return false, before, nil
	}
	row := tx.tx.QueryRowContext(tx.ctx, `UPDATE canaries SET fired_at = NULL, armed_at = ? WHERE key_id = ? RETURNING `+canaryColumns,
		toMilli(at), keyID)
	c, err = scanCanary(row, "reset canary "+keyID)
	return err == nil, c, err
}

func scanCanary(row scanner, what string) (Canary, error) {
	var c Canary
	var armed int64
	var fired, lastFired, asn sql.NullInt64
	var ip, country, asOrg, client sql.NullString
	err := row.Scan(&c.KeyID, &c.Planted, &c.IgnoreJSON, &armed, &fired, &lastFired, &c.Fires, &ip, &country, &asn, &asOrg, &client)
	if err != nil {
		return Canary{}, typed(err, what)
	}
	c.ArmedAt, c.FiredAt, c.LastFiredAt = fromMilli(armed), timeOf(fired), timeOf(lastFired)
	if ip.Valid {
		c.Last.Address, _ = netip.ParseAddr(ip.String)
	}
	c.Last.Country, c.Last.ASN, c.Last.ASOrg, c.Last.Client = country.String, asn.Int64, asOrg.String, client.String
	return c, nil
}
