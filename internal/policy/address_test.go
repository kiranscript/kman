package policy_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/policy"
)

func TestAddressLocksAreKeptAndNormalized(t *testing.T) {
	version, got, err := policy.Normalize([]byte(`{"allowed_countries":["de","US"],"allowed_cidrs":["203.0.113.0/24","198.51.100.7","2001:db8::/32"]}`))
	if err != nil {
		t.Fatal(err)
	}
	// A bare address is a network of one, and a country code is in capitals.
	want := `{"allowed_cidrs":["203.0.113.0/24","198.51.100.7/32","2001:db8::/32"],"allowed_countries":["DE","US"]}`
	if string(got) != want || version != 1 {
		t.Errorf("Normalize = %d, %s; want 1, %s", version, got, want)
	}
	p, err := policy.Parse(string(got))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AllowedCIDRs) != 3 || len(p.AllowedCountries) != 2 {
		t.Errorf("Parse = %+v", p)
	}
	if p.RequiresMetering() {
		t.Error("an address lock does not need metering")
	}
}

func TestAnEmptyLockIsNoLock(t *testing.T) {
	_, got, err := policy.Normalize([]byte(`{"allowed_cidrs":[],"allowed_countries":[]}`))
	if err != nil || string(got) != `{}` {
		t.Errorf("Normalize = %s, %v; want {}", got, err)
	}
}

// A lock that would silently lock nothing, or never match, is refused.
func TestNormalizeRefusesABadAddressLock(t *testing.T) {
	for raw, hint := range map[string]string{
		`{"allowed_cidrs":["not-an-address"]}`:          "not-an-address",
		`{"allowed_cidrs":[""]}`:                        "allowed_cidrs",
		`{"allowed_cidrs":["10.0.0.5/8"]}`:              "10.0.0.0/8", // host bits: the network it means is named
		`{"allowed_cidrs":["0.0.0.0/0"]}`:               "leave",      // every address is no lock
		`{"allowed_cidrs":["::/0"]}`:                    "leave",
		`{"allowed_cidrs":["::ffff:10.0.0.0/104"]}`:     "IPv4", // never matches: clients are unmapped
		`{"allowed_cidrs":["10.0.0.0/8","10.0.0.0/8"]}`: "twice",
		`{"allowed_cidrs":["fe80::1%eth0"]}`:            "fe80",
		`{"allowed_cidrs":["10.0.0.0/33"]}`:             "10.0.0.0/33",
		`{"allowed_countries":["USA"]}`:                 "USA",
		`{"allowed_countries":["U"]}`:                   "\"U\"",
		`{"allowed_countries":["1A"]}`:                  "1A",
		`{"allowed_countries":[""]}`:                    "allowed_countries",
		`{"allowed_countries":["US","us"]}`:             "twice",
		`{"allowed_countries":"US"}`:                    "allowed_countries",
		`{"allowed_cidrs":"10.0.0.0/8"}`:                "allowed_cidrs",
		`{"allowed_countries":["é"]}`:                   "é",
	} {
		_, got, err := policy.Normalize([]byte(raw))
		if err == nil {
			t.Errorf("Normalize(%s) = %s, want an error", raw, got)
			continue
		}
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("Normalize(%s): the error %q does not mention %q", raw, err, hint)
		}
	}
}

func TestTooManyLocksAreRefused(t *testing.T) {
	var cidrs, countries []string
	for i := range 65 {
		cidrs = append(cidrs, netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(i), 0, 0}), 16).String())
	}
	if _, _, err := policy.Normalize([]byte(`{"allowed_cidrs":["` + strings.Join(cidrs, `","`) + `"]}`)); err == nil {
		t.Error("65 networks were accepted")
	}
	for a := 'A'; a <= 'Z' && len(countries) < 251; a++ {
		for b := 'A'; b <= 'Z' && len(countries) < 251; b++ {
			countries = append(countries, string([]rune{a, b}))
		}
	}
	if _, _, err := policy.Normalize([]byte(`{"allowed_countries":["` + strings.Join(countries, `","`) + `"]}`)); err == nil {
		t.Error("251 countries were accepted")
	}
}

func origin(addr, country string) policy.Origin {
	var a netip.Addr
	if addr != "" {
		a = netip.MustParseAddr(addr)
	}
	return policy.Origin{Addr: a, Country: country}
}

func TestEvaluateOrigin(t *testing.T) {
	const notAllowed = policy.AddressNotAllowed
	for _, tc := range []struct {
		name   string
		policy string
		origin policy.Origin
		want   policy.Decision
	}{
		{"no lock allows any address", `{}`, origin("198.51.100.1", "US"), policy.Allow},
		{"no lock allows an unknown address", `{}`, origin("", ""), policy.Allow},
		{"inside a network", `{"allowed_cidrs":["203.0.113.0/24"]}`, origin("203.0.113.77", ""), policy.Allow},
		{"outside every network", `{"allowed_cidrs":["203.0.113.0/24"]}`, origin("203.0.114.1", ""), notAllowed},
		{"the second network", `{"allowed_cidrs":["10.0.0.0/8","203.0.113.0/24"]}`, origin("203.0.113.1", ""), policy.Allow},
		{"the network's edges", `{"allowed_cidrs":["203.0.113.0/24"]}`, origin("203.0.113.255", ""), policy.Allow},
		{"a single address", `{"allowed_cidrs":["203.0.113.5"]}`, origin("203.0.113.5", ""), policy.Allow},
		{"and not its neighbour", `{"allowed_cidrs":["203.0.113.5"]}`, origin("203.0.113.6", ""), notAllowed},
		{"IPv6", `{"allowed_cidrs":["2001:db8::/32"]}`, origin("2001:db8:1::9", ""), policy.Allow},
		{"IPv6 outside", `{"allowed_cidrs":["2001:db8::/32"]}`, origin("2001:db9::1", ""), notAllowed},
		{"IPv4 is not an IPv6 network", `{"allowed_cidrs":["2001:db8::/32"]}`, origin("203.0.113.1", ""), notAllowed},
		{"an IPv4-mapped client is its IPv4 address", `{"allowed_cidrs":["203.0.113.0/24"]}`, origin("::ffff:203.0.113.9", ""), policy.Allow},
		{"an unknown address fails a network lock", `{"allowed_cidrs":["203.0.113.0/24"]}`, origin("", ""), notAllowed},

		{"an allowed country", `{"allowed_countries":["DE","US"]}`, origin("198.51.100.1", "US"), policy.Allow},
		{"another country", `{"allowed_countries":["DE","US"]}`, origin("198.51.100.1", "FR"), notAllowed},
		{"an unknown country fails a country lock", `{"allowed_countries":["US"]}`, origin("198.51.100.1", ""), notAllowed},
		{"a country is not a prefix match", `{"allowed_countries":["US"]}`, origin("198.51.100.1", "USA"), notAllowed},

		{"both locks must hold: both do", `{"allowed_cidrs":["203.0.113.0/24"],"allowed_countries":["US"]}`, origin("203.0.113.1", "US"), policy.Allow},
		{"the network is right and the country wrong", `{"allowed_cidrs":["203.0.113.0/24"],"allowed_countries":["US"]}`, origin("203.0.113.1", "DE"), notAllowed},
		{"the country is right and the network wrong", `{"allowed_cidrs":["203.0.113.0/24"],"allowed_countries":["US"]}`, origin("198.51.100.1", "US"), notAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.EvaluateOrigin(tc.origin, compile(t, tc.policy)); got != tc.want {
				t.Errorf("EvaluateOrigin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTheDecisionNamesTheErrorCode(t *testing.T) {
	if got := policy.AddressNotAllowed.String(); got != "address_not_allowed" {
		t.Errorf("String = %q", got)
	}
}

func TestACompiledPolicyReportsWhatItLocks(t *testing.T) {
	for _, tc := range []struct {
		policy            string
		locked, countries bool
	}{
		{`{}`, false, false},
		{`{"allowed_cidrs":["10.0.0.0/8"]}`, true, false},
		{`{"allowed_countries":["US"]}`, true, true},
		{`{"allowed_cidrs":["10.0.0.0/8"],"allowed_countries":["US"]}`, true, true},
	} {
		c := compile(t, tc.policy)
		if c.AddressLocked() != tc.locked || c.CountryLocked() != tc.countries {
			t.Errorf("%s: AddressLocked, CountryLocked = %v, %v", tc.policy, c.AddressLocked(), c.CountryLocked())
		}
	}
	var nilC *policy.Compiled
	if nilC.AddressLocked() || nilC.CountryLocked() {
		t.Error("a nil policy locks nothing here; Evaluate is what refuses it")
	}
}

// A policy that can't be read allows nothing, which Evaluate says; the address
// check leaves that to it so the one refusal carries the reason.
func TestAnUnreadablePolicyIsLeftToEvaluate(t *testing.T) {
	c, _ := policy.Compile(99, `{"allowed_cidrs":["10.0.0.0/8"]}`)
	if got := policy.EvaluateOrigin(origin("198.51.100.1", ""), c); got != policy.Allow {
		t.Errorf("EvaluateOrigin = %v", got)
	}
	if got := policy.EvaluateOrigin(origin("198.51.100.1", ""), nil); got != policy.Allow {
		t.Errorf("EvaluateOrigin(nil) = %v", got)
	}
}
