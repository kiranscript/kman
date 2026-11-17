package gateway

import (
	"net/netip"

	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/policy/geoip"
)

// Geo places an address: its country and its ASN. *geoip.DB is one.
type Geo interface {
	Lookup(netip.Addr) geoip.Info
}

// address holds a request to its key's address locks (CIDR and country). It
// runs before the body is read, so an address that may not use the key is
// refused without kman taking what it sends. With a GeoIP database it also
// places every request, whether the key is locked or not, for the request's
// event (#46).
//
// A country lock that kman has no way to check is refused, never allowed: with
// no database, or with one that holds no country for the address. The first is
// logged, since the operator has something to fix.
func (g *Gateway) address(x *exchange) *refusal {
	c := x.route.Key.Compiled
	if g.opts.Geo != nil {
		x.geo = g.opts.Geo.Lookup(x.ip)
	}
	if !c.AddressLocked() {
		return nil
	}
	if c.CountryLocked() && g.opts.Geo == nil {
		g.opts.Log.Error("a key is locked to countries and kman has no GeoIP database, so it refuses the key's requests: set geoip in kman.yaml and run kman geoip update", "key", x.route.Key.ID)
	}
	if policy.EvaluateOrigin(policy.Origin{Addr: x.ip, Country: x.geo.Country}, c) == policy.AddressNotAllowed {
		return &refusal{code: codeAddressNotAllowed}
	}
	return nil
}
