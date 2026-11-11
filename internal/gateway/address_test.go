package gateway_test

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/policy/geoip"
)

// places is a Geo that knows a few addresses, and counts what it is asked.
type places struct {
	mu    sync.Mutex
	known map[string]geoip.Info
	asked []netip.Addr
}

func (p *places) Lookup(a netip.Addr) geoip.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, a)
	return p.known[a.String()]
}

func withGeo(g gateway.Geo) func(*gateway.Options) { return func(o *gateway.Options) { o.Geo = g } }

func trustLoopback(o *gateway.Options) {
	o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
}

func forwardedFor(secret, ip string) http.Header {
	h := withBearer(secret)
	h.Set("X-Forwarded-For", ip)
	return h
}

func TestAKeyLockedToNetworksFailsFromAnywhereElse(t *testing.T) {
	// The test's client is 127.0.0.1.
	s, up, secret := guarded(t, `{"allowed_cidrs":["203.0.113.0/24","127.0.0.0/8"]}`, []string{"* /**"})
	if resp, body := s.send("GET", "/v1/models", "", withBearer(secret)); resp.StatusCode != 200 {
		t.Fatalf("from an allowed network = %d %s", resp.StatusCode, body)
	}
	up.next(t)

	s, up, secret = guarded(t, `{"allowed_cidrs":["203.0.113.0/24"]}`, []string{"* /**"})
	resp, body := s.send("GET", "/v1/models", "", withBearer(secret))
	wantRefusal(t, resp, body, http.StatusForbidden, "address_not_allowed")
	up.none(t)
}

func TestTheLockHoldsTheClientAddressFromTrustedProxiesOnly(t *testing.T) {
	const lock = `{"allowed_cidrs":["203.0.113.0/24"]}`
	t.Run("a trusted proxy reports the client", func(t *testing.T) {
		s, up, secret := guarded(t, lock, []string{"* /**"}, trustLoopback)
		if resp, body := s.send("GET", "/v1/x", "", forwardedFor(secret, "203.0.113.7")); resp.StatusCode != 200 {
			t.Fatalf("an allowed client behind the proxy = %d %s", resp.StatusCode, body)
		}
		up.next(t)
		resp, body := s.send("GET", "/v1/x", "", forwardedFor(secret, "198.51.100.7"))
		wantRefusal(t, resp, body, http.StatusForbidden, "address_not_allowed")
		up.none(t)
	})
	t.Run("what the client claims is not believed", func(t *testing.T) {
		// A thief adds the header himself, and no proxy of ours is in front.
		s, up, secret := guarded(t, lock, []string{"* /**"})
		resp, body := s.send("GET", "/v1/x", "", forwardedFor(secret, "203.0.113.7"))
		wantRefusal(t, resp, body, http.StatusForbidden, "address_not_allowed")
		up.none(t)
	})
}

func TestAKeyLockedToCountriesFailsFromAnywhereElse(t *testing.T) {
	geo := &places{known: map[string]geoip.Info{
		"203.0.113.7":  {Country: "US", ASN: 64500},
		"198.51.100.7": {Country: "FR", ASN: 64501},
		"192.0.2.1":    {}, // in no database
	}}
	s, up, secret := guarded(t, `{"allowed_countries":["US","DE"]}`, []string{"* /**"}, withGeo(geo), trustLoopback)

	if resp, body := s.send("GET", "/v1/x", "", forwardedFor(secret, "203.0.113.7")); resp.StatusCode != 200 {
		t.Fatalf("from an allowed country = %d %s", resp.StatusCode, body)
	}
	up.next(t)
	for _, ip := range []string{"198.51.100.7", "192.0.2.1"} { // another country; and one no database knows
		resp, body := s.send("GET", "/v1/x", "", forwardedFor(secret, ip))
		wantRefusal(t, resp, body, http.StatusForbidden, "address_not_allowed")
		up.none(t)
	}
}

// A country lock the gateway cannot check is not a lock that lets everything in.
func TestACountryLockWithNoDatabaseRefusesEveryRequest(t *testing.T) {
	s, up, secret := guarded(t, `{"allowed_countries":["US"]}`, []string{"* /**"})
	resp, body := s.send("GET", "/v1/x", "", withBearer(secret))
	wantRefusal(t, resp, body, http.StatusForbidden, "address_not_allowed")
	up.none(t)
	if !strings.Contains(s.logs.String(), "GeoIP") {
		t.Errorf("the log does not say why: %s", s.logs)
	}
}

func TestACountryLockWithNoCountryDatabaseRefusesToo(t *testing.T) {
	// A database with ASNs only: every lookup finds no country.
	geo := &places{known: map[string]geoip.Info{"127.0.0.1": {ASN: 64500}}}
	s, up, secret := guarded(t, `{"allowed_countries":["US"]}`, []string{"* /**"}, withGeo(geo))
	resp, body := s.send("GET", "/v1/x", "", withBearer(secret))
	wantRefusal(t, resp, body, http.StatusForbidden, "address_not_allowed")
	up.none(t)
}

func TestTheAddressIsCheckedBeforeTheBodyIsRead(t *testing.T) {
	s, up, secret := guarded(t, `{"allowed_cidrs":["203.0.113.0/24"]}`, []string{"* /**"}, func(o *gateway.Options) { o.MaxBody = 8 })
	resp, body := s.send("POST", "/v1/x", strings.Repeat("x", 64), withBearer(secret))
	wantRefusal(t, resp, body, http.StatusForbidden, "address_not_allowed") // not request_too_large
	up.none(t)
}

func TestAnAddressIsLookedUpOnlyWhenADatabaseIsGiven(t *testing.T) {
	// No panic and no lookup without one, for a key with no lock at all.
	s, up, secret := guarded(t, `{}`, []string{"* /**"})
	if resp, body := s.send("GET", "/v1/x", "", withBearer(secret)); resp.StatusCode != 200 {
		t.Fatalf("= %d %s", resp.StatusCode, body)
	}
	up.next(t)
}

// The country and ASN of a request are what its event will carry (#46).
func TestEveryRequestIsPlacedWhenADatabaseIsGiven(t *testing.T) {
	geo := &places{known: map[string]geoip.Info{"203.0.113.7": {Country: "US", ASN: 64500}}}
	type seen struct {
		ip  netip.Addr
		geo geoip.Info
	}
	var got []seen
	var mu sync.Mutex
	s, up, secret := guarded(t, `{}`, []string{"* /**"}, withGeo(geo), trustLoopback, func(o *gateway.Options) {
		gateway.ObserveEmit(o, func(ip netip.Addr, g geoip.Info) {
			mu.Lock()
			got = append(got, seen{ip, g})
			mu.Unlock()
		})
	})
	if resp, body := s.send("GET", "/v1/x", "", forwardedFor(secret, "203.0.113.7")); resp.StatusCode != 200 {
		t.Fatalf("= %d %s", resp.StatusCode, body)
	}
	up.next(t)
	s.front.Close() // the handler has returned, so its event was emitted
	mu.Lock()
	defer mu.Unlock()
	want := seen{netip.MustParseAddr("203.0.113.7"), geoip.Info{Country: "US", ASN: 64500}}
	if len(got) != 1 || got[0] != want {
		t.Errorf("emitted %+v, want [%+v]", got, want)
	}
}
