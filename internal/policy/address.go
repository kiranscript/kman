package policy

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Limits of the address locks.
const (
	maxCIDRs     = 64
	maxCountries = 250 // ISO 3166-1 has 249 codes
)

// normalizeLocks checks a policy's address locks as they were written and
// returns them in the form kman keeps: networks in CIDR form, with a bare
// address a network of one, and country codes in capitals. A lock that would
// lock nothing, or that no request could match, is refused.
func normalizeLocks(cidrs, countries []string) (nets, codes []string, err error) {
	if len(cidrs) > maxCIDRs {
		return nil, nil, fmt.Errorf("allowed_cidrs: at most %d networks", maxCIDRs)
	}
	for _, s := range cidrs {
		p, err := parseNetwork(s)
		if err != nil {
			return nil, nil, fmt.Errorf("allowed_cidrs %q: %w", s, err)
		}
		n := p.String()
		if slices.Contains(nets, n) {
			return nil, nil, fmt.Errorf("allowed_cidrs: %q is listed twice", n)
		}
		nets = append(nets, n)
	}
	if len(countries) > maxCountries {
		return nil, nil, fmt.Errorf("allowed_countries: at most %d countries", maxCountries)
	}
	for _, s := range countries {
		if len(s) != 2 || !isLetter(s[0]) || !isLetter(s[1]) {
			return nil, nil, fmt.Errorf("allowed_countries %q: use a two-letter ISO 3166-1 code such as US", s)
		}
		c := strings.ToUpper(s)
		if slices.Contains(codes, c) {
			return nil, nil, fmt.Errorf("allowed_countries: %q is listed twice", c)
		}
		codes = append(codes, c)
	}
	return nets, codes, nil
}

func isLetter(b byte) bool { return b|0x20 >= 'a' && b|0x20 <= 'z' }

// parseNetwork reads a network such as 203.0.113.0/24, or a bare address as a
// network of one.
func parseNetwork(s string) (netip.Prefix, error) {
	if s == "" {
		return netip.Prefix{}, errors.New("a network is not empty")
	}
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, errors.New("use an address or a network such as 203.0.113.0/24")
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, errors.New("use an address or a network such as 203.0.113.0/24")
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	switch {
	case p.Addr().Is4In6():
		return netip.Prefix{}, errors.New("write an IPv4 network as IPv4, such as 203.0.113.0/24: kman sees an IPv4 client as one")
	case p.Bits() == 0:
		return netip.Prefix{}, errors.New("this is every address; to allow every address, leave the lock out")
	case p.Masked() != p:
		return netip.Prefix{}, fmt.Errorf("it has bits set past its prefix; did you mean %s?", p.Masked())
	}
	return p, nil
}

// Origin is where a request came from, which is what an address lock is held to.
type Origin struct {
	// Addr is the client's address, invalid when kman has none.
	Addr netip.Addr
	// Country is the client address's ISO 3166-1 code in capitals, empty when no
	// GeoIP database says. Only a policy with a country lock reads it
	// (Compiled.CountryLocked), so a caller may skip the lookup without one.
	Country string
}

// AddressLocked reports whether the policy locks the key to networks or
// countries.
func (c *Compiled) AddressLocked() bool {
	return c != nil && (len(c.cidrs) > 0 || len(c.countries) > 0)
}

// CountryLocked reports whether the policy locks the key to countries, which
// needs a GeoIP database to hold anything to.
func (c *Compiled) CountryLocked() bool { return c != nil && len(c.countries) > 0 }

// EvaluateOrigin holds a request to its key's address locks. It is Allow or
// AddressNotAllowed, and pure like Evaluate. When a key has both locks, the
// address must satisfy both. An address kman cannot place, or a country no
// database names, fails the lock it is checked against: a lock never lets what
// it can't check through.
//
// It is apart from Evaluate so that a gateway can refuse an address before it
// reads the request body. A policy that can't be read is left to Evaluate,
// which refuses everything.
func EvaluateOrigin(o Origin, c *Compiled) Decision {
	if c == nil || c.broken != nil {
		return Allow
	}
	if len(c.cidrs) > 0 {
		a := o.Addr.Unmap()
		if !a.IsValid() || !slices.ContainsFunc(c.cidrs, func(p netip.Prefix) bool { return p.Contains(a) }) {
			return AddressNotAllowed
		}
	}
	if len(c.countries) > 0 && !slices.Contains(c.countries, o.Country) {
		return AddressNotAllowed
	}
	return Allow
}
