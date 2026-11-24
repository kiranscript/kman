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
// event (#46). A request refused before this stage is placed when its event is
// emitted, if it proved a key (see place).
//
// A country lock that kman has no way to check is refused, never allowed: with
// no database, or with one that holds no country for the address. The first is
// logged, since the operator has something to fix.
func (g *Gateway) address(x *exchange) *refusal {
	c := x.route.Key.Compiled
	g.place(x)
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

// place looks up where the request came from, once. A request with no key it
// proved is not placed: its event belongs to no key, and a flood of made-up keys
// must stay cheap.
func (g *Gateway) place(x *exchange) {
	if x.placed || g.opts.Geo == nil || x.keyID == "" {
		return
	}
	x.geo, x.placed = g.opts.Geo.Lookup(x.ip), true
}
