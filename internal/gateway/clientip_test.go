package gateway_test

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/kirank55/kman/internal/gateway"
)

func prefixes(t *testing.T, s ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, p := range s {
		pf, err := netip.ParsePrefix(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, pf)
	}
	return out
}

func TestTheClientAddressIsTheConnectionsUnlessATrustedProxyVouches(t *testing.T) {
	proxies := prefixes(t, "10.0.0.0/8", "fd00::/8")
	for _, tc := range []struct {
		name    string
		peer    string
		xff     []string
		trusted []netip.Prefix
		want    string
	}{
		{"a direct connection", "203.0.113.5:4000", nil, nil, "203.0.113.5"},
		{"X-Forwarded-For is ignored with no trusted proxies", "203.0.113.5:4000", []string{"198.51.100.9"}, nil, "203.0.113.5"},
		{"and from a peer that isn't trusted", "203.0.113.5:4000", []string{"198.51.100.9"}, proxies, "203.0.113.5"},
		{"a trusted proxy's client", "10.1.2.3:4000", []string{"198.51.100.9"}, proxies, "198.51.100.9"},
		{"a trusted proxy and no header", "10.1.2.3:4000", nil, proxies, "10.1.2.3"},
		{"the client can't choose its own address", "10.1.2.3:4000", []string{"1.1.1.1, 198.51.100.9"}, proxies, "198.51.100.9"},
		{"through two trusted proxies", "10.1.2.3:4000", []string{"198.51.100.9, 10.9.9.9"}, proxies, "198.51.100.9"},
		{"the rightmost untrusted address", "10.1.2.3:4000", []string{"1.1.1.1, 198.51.100.9, 192.0.2.8, 10.9.9.9"}, proxies, "192.0.2.8"},
		{"several header lines are one list", "10.1.2.3:4000", []string{"1.1.1.1", "198.51.100.9, 10.9.9.9"}, proxies, "198.51.100.9"},
		{"every hop trusted: the first", "10.1.2.3:4000", []string{"10.5.5.5, 10.9.9.9"}, proxies, "10.5.5.5"},
		{"an entry that isn't an address: the proxy's", "10.1.2.3:4000", []string{"198.51.100.9, nonsense"}, proxies, "10.1.2.3"},
		{"an empty header", "10.1.2.3:4000", []string{""}, proxies, "10.1.2.3"},
		{"an address with a port", "10.1.2.3:4000", []string{"198.51.100.9:5555"}, proxies, "198.51.100.9"},
		{"an IPv6 address with a port", "10.1.2.3:4000", []string{"[2001:db8::7]:5555"}, proxies, "2001:db8::7"},
		{"an IPv6 client", "[fd00::1]:4000", []string{"2001:db8::7"}, proxies, "2001:db8::7"},
		{"an IPv4-mapped peer is its IPv4 address", "[::ffff:10.1.2.3]:4000", []string{"198.51.100.9"}, proxies, "198.51.100.9"},
		{"an IPv4-mapped client is its IPv4 address", "10.1.2.3:4000", []string{"::ffff:198.51.100.9"}, proxies, "198.51.100.9"},
		{"a zone is dropped", "10.1.2.3:4000", []string{"fe80::1%eth0"}, proxies, "fe80::1"},
		{"spaces around entries", "10.1.2.3:4000", []string{"  198.51.100.9  ,  10.9.9.9 "}, proxies, "198.51.100.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tc.peer, Header: http.Header{}}
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			got := gateway.ClientIP(r, tc.trusted)
			if got.String() != tc.want {
				t.Errorf("ClientIP = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAConnectionWithNoAddressHasNoClientAddress(t *testing.T) {
	for _, peer := range []string{"", "@", "unix", "not an address"} {
		r := &http.Request{RemoteAddr: peer, Header: http.Header{"X-Forwarded-For": {"198.51.100.9"}}}
		if got := gateway.ClientIP(r, prefixes(t, "0.0.0.0/0")); got.IsValid() {
			t.Errorf("ClientIP(peer %q) = %v, want none", peer, got)
		}
	}
}

// The client address is worked out for every request, so for the usual one, a
// connection with no proxy, it allocates nothing.
func TestTheClientAddressOfAConnectionAllocatesNothing(t *testing.T) {
	r := &http.Request{RemoteAddr: "203.0.113.5:4000", Header: http.Header{}}
	if allocs := testing.AllocsPerRun(100, func() { gateway.ClientIP(r, nil) }); allocs != 0 {
		t.Errorf("ClientIP allocated %v times", allocs)
	}
	r.RemoteAddr = "[2001:db8::1]:4000"
	if allocs := testing.AllocsPerRun(100, func() { gateway.ClientIP(r, nil) }); allocs != 0 {
		t.Errorf("ClientIP allocated %v times for IPv6", allocs)
	}
}
