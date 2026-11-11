package gateway_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/limiter"
	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/token"
)

// withPolicy is a key holding the policy, as admin would publish it.
func withPolicy(t testing.TB, raw string) (registry.Key, string) {
	t.Helper()
	k, secret := newKey(t)
	version, normalized, err := policy.Normalize([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	k.Policy, k.PolicyVersion = string(normalized), version
	return k, secret
}

// guarded is a stack in front of a recording upstream, with one key holding the
// policy, whose upstream allows the paths.
func guarded(t *testing.T, raw string, paths []string, edit ...func(*gateway.Options)) (*stack, *recording, string) {
	t.Helper()
	up := newRecording()
	s, _ := behind(t, up, edit...)
	k, secret := withPolicy(t, raw)
	spec := specOfRoute(t, s)
	spec.Paths = paths
	s.publish(spec, k)
	return s, up, secret
}

func TestAnUpstreamsPathAllowlistHoldsEveryKeyOnIt(t *testing.T) {
	s, up, secret := guarded(t, `{}`, []string{"POST /v1/chat/completions", "GET /v1/models"})

	for _, uri := range []string{"/v1/chat/completions", "/v1/models"} {
		method := "POST"
		if strings.HasSuffix(uri, "models") {
			method = "GET"
		}
		if resp, body := s.send(method, uri, "", withBearer(secret)); resp.StatusCode != 200 {
			t.Errorf("%s %s = %d %s", method, uri, resp.StatusCode, body)
		}
		up.next(t)
	}
	for _, tc := range []struct{ method, uri string }{
		{"GET", "/v1/chat/completions"},
		{"POST", "/v1/batches"},
		{"DELETE", "/v1/models"},
		{"GET", "/v1/models/../files"},
		{"GET", "/v1/models/%2e%2e/files"},
		{"GET", "//v1/models"},
		{"GET", "/v1/models/"},
		{"GET", "/v1/a%2Fb"},
	} {
		resp, body := s.send(tc.method, tc.uri, "", withBearer(secret))
		wantRefusal(t, resp, body, http.StatusForbidden, "path_not_allowed")
		up.none(t)
	}
}

func TestAKeysOwnPathsNarrowItsUpstreamsList(t *testing.T) {
	s, up, secret := guarded(t, `{"allowed_paths":["POST /v1/chat/completions"]}`, []string{"* /**"})
	if resp, body := s.send("POST", "/v1/chat/completions", "{}", withBearer(secret)); resp.StatusCode != 200 {
		t.Errorf("allowed route = %d %s", resp.StatusCode, body)
	}
	up.next(t)
	resp, body := s.send("GET", "/v1/models", "", withBearer(secret))
	wantRefusal(t, resp, body, http.StatusForbidden, "path_not_allowed")
	up.none(t)
}

func TestAKeyWithNoModelAllowlistMayBeGivenARouteTheUpstreamLeavesOut(t *testing.T) {
	s, up, secret := guarded(t, `{"allowed_paths":["POST /v1/batches"]}`, []string{"POST /v1/chat/completions"})
	if resp, body := s.send("POST", "/v1/batches", "{}", withBearer(secret)); resp.StatusCode != 200 {
		t.Errorf("the added route = %d %s", resp.StatusCode, body)
	}
	up.next(t)
}

func chat(model string) string { return `{"model":"` + model + `","messages":[]}` }

func TestAModelAllowlistRefusesAnyOtherModel(t *testing.T) {
	s, up, secret := guarded(t, `{"allowed_models":["gpt-4o","claude-*"]}`, []string{"* /**"})
	for _, m := range []string{"gpt-4o", "claude-sonnet-4"} {
		if resp, body := s.send("POST", "/v1/chat/completions", chat(m), withBearer(secret)); resp.StatusCode != 200 {
			t.Errorf("model %s = %d %s", m, resp.StatusCode, body)
		}
		up.next(t)
	}
	for _, m := range []string{"gpt-4o-mini", "o1", "GPT-4O", ""} {
		resp, body := s.send("POST", "/v1/chat/completions", chat(m), withBearer(secret))
		wantRefusal(t, resp, body, http.StatusForbidden, "model_not_allowed")
		up.none(t)
	}
}

// A request kman can't read a model from could name any model upstream, so a
// model allowlist can't hold it: it is an unmetered request (SPEC §8).
func TestAModelAllowlistRefusesWhatItCannotReadAModelFrom(t *testing.T) {
	s, up, secret := guarded(t, `{"allowed_models":["gpt-4o"]}`, []string{"* /**"})
	for name, body := range map[string]string{
		"no body":                           "",
		"no model":                          `{"input":"x"}`,
		"a model that repeats":              `{"model":"gpt-4o","model":"gpt-4o-mini"}`,
		"a model in other case":             `{"model":"gpt-4o","Model":"gpt-4o-mini"}`,
		"a model that isn't text":           `{"model":["gpt-4o"]}`,
		"not JSON":                          "model=gpt-4o",
		"a model hidden in a nested object": `{"x":{"model":"gpt-4o"}}`,
	} {
		method := "POST"
		if body == "" {
			method = "GET"
		}
		resp, got := s.send(method, "/v1/x", body, withBearer(secret))
		t.Run(name, func(t *testing.T) {
			wantRefusal(t, resp, got, http.StatusForbidden, "unmetered_request")
			up.none(t)
		})
	}
}

func TestAKeyWithNoModelAllowlistForwardsAnyBody(t *testing.T) {
	s, up, secret := guarded(t, `{}`, []string{"* /**"})
	if resp, body := s.send("POST", "/v1/x", "not json at all", withBearer(secret)); resp.StatusCode != 200 {
		t.Errorf("= %d %s", resp.StatusCode, body)
	}
	if got := up.next(t); got.body != "not json at all" {
		t.Errorf("the upstream got %q", got.body)
	}
}

func TestAPolicyThatCannotBeReadAllowsNothingAndIsLogged(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up)
	k, secret := newKey(t)
	k.Policy, k.PolicyVersion = `{"from":"a newer kman"}`, 99
	s.publish(specOfRoute(t, s), k)
	resp, body := s.send("GET", "/v1/x", "", withBearer(secret))
	wantRefusal(t, resp, body, http.StatusForbidden, "path_not_allowed")
	up.none(t)
	if !strings.Contains(s.logs.String(), k.ID) || strings.Contains(s.logs.String(), "/v1/x") {
		t.Errorf("the log should name the key and not the path: %s", s.logs)
	}
}

func TestAKeyOverItsRPMLimitIsRefusedUntilTheWindowPasses(t *testing.T) {
	s, up, secret := guarded(t, `{"rpm_limit":3}`, []string{"* /**"})
	for i := 0; i < 3; i++ {
		if resp, body := s.send("GET", "/v1/x", "", withBearer(secret)); resp.StatusCode != 200 {
			t.Fatalf("request %d = %d %s", i+1, resp.StatusCode, body)
		}
		up.next(t)
	}
	resp, body := s.send("GET", "/v1/x", "", withBearer(secret))
	wantRefusal(t, resp, body, http.StatusTooManyRequests, "rate_limited")
	if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || n < 1 || n > 60 {
		t.Errorf("Retry-After = %q", resp.Header.Get("Retry-After"))
	}
	up.none(t)

	s.now = s.now.Add(2 * time.Minute)
	if resp, body := s.send("GET", "/v1/x", "", withBearer(secret)); resp.StatusCode != 200 {
		t.Errorf("after the window = %d %s", resp.StatusCode, body)
	}
}

func TestOneKeysLimitDoesNotLimitAnother(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up)
	slow, slowSecret := withPolicy(t, `{"rpm_limit":1}`)
	free, freeSecret := withPolicy(t, `{}`)
	s.publish(specOfRoute(t, s), slow, free)
	s.send("GET", "/v1/x", "", withBearer(slowSecret))
	if resp, body := s.send("GET", "/v1/x", "", withBearer(slowSecret)); resp.StatusCode != 429 {
		t.Fatalf("the slow key = %d %s", resp.StatusCode, body)
	}
	for i := 0; i < 5; i++ {
		if resp, body := s.send("GET", "/v1/x", "", withBearer(freeSecret)); resp.StatusCode != 200 {
			t.Fatalf("the free key = %d %s", resp.StatusCode, body)
		}
	}
}

func TestARefusedRequestDoesNotSpendTheKeysRate(t *testing.T) {
	s, up, secret := guarded(t, `{"rpm_limit":2}`, []string{"GET /v1/ok"})
	for i := 0; i < 10; i++ {
		s.send("GET", "/v1/not-ok", "", withBearer(secret))
	}
	for i := 0; i < 2; i++ {
		if resp, body := s.send("GET", "/v1/ok", "", withBearer(secret)); resp.StatusCode != 200 {
			t.Fatalf("request %d = %d %s", i+1, resp.StatusCode, body)
		}
		up.next(t)
	}
}

func badKey(t testing.TB) string {
	t.Helper()
	m, err := token.MintVirtualKey(token.NewKeyID())
	if err != nil {
		t.Fatal(err)
	}
	return m.Reveal()
}

func TestAnAddressPresentingInvalidKeysIsThrottledEvenForAGoodOne(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up, func(o *gateway.Options) {
		o.Limiter = limiter.New(limiter.Options{Now: func() time.Time { return t0 }, InvalidPerMinute: 3})
	})
	for i := 0; i < 3; i++ {
		resp, body := s.send("GET", "/v1/x", "", withBearer(badKey(t)))
		wantRefusal(t, resp, body, http.StatusUnauthorized, "invalid_key")
	}
	resp, body := s.send("GET", "/v1/x", "", withBearer(badKey(t)))
	wantRefusal(t, resp, body, http.StatusTooManyRequests, "rate_limited")
	if resp.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
	if !strings.Contains(body, "address") {
		t.Errorf("the message should say it is the address, not the key: %s", body)
	}
	resp, body = s.send("GET", "/v1/x", "", withBearer(secret))
	wantRefusal(t, resp, body, http.StatusTooManyRequests, "rate_limited")
	up.none(t)
}

func TestInvalidKeysAreCountedWhateverMakesThemInvalid(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up, func(o *gateway.Options) {
		o.Limiter = limiter.New(limiter.Options{Now: func() time.Time { return t0 }, InvalidPerMinute: 4})
	})
	good, goodSecret := newKey(t)
	s.publish(specOfRoute(t, s), good)
	for _, h := range []http.Header{
		{},                             // no key
		withBearer("kmn_nonsense"),     // doesn't parse
		withBearer(tamper(goodSecret)), // checksum
		withBearer(badKey(t)),          // unknown key
	} {
		resp, body := s.send("GET", "/v1/x", "", h)
		wantRefusal(t, resp, body, http.StatusUnauthorized, "invalid_key")
	}
	resp, body := s.send("GET", "/v1/x", "", withBearer(goodSecret))
	wantRefusal(t, resp, body, http.StatusTooManyRequests, "rate_limited")
}

// Revoked, expired and disabled keys show their holder has the secret, which a
// guesser doesn't.
func TestAKeyOfAnotherStateIsNotAnInvalidKey(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up, func(o *gateway.Options) {
		o.Limiter = limiter.New(limiter.Options{Now: func() time.Time { return t0 }, InvalidPerMinute: 2})
	})
	revoked, revokedSecret := newKey(t)
	revoked.Status = "revoked"
	s.publish(specOfRoute(t, s), revoked)
	for i := 0; i < 10; i++ {
		resp, body := s.send("GET", "/v1/x", "", withBearer(revokedSecret))
		wantRefusal(t, resp, body, http.StatusUnauthorized, "key_revoked")
	}
}

func TestTheThrottleIsPerAddressAndTheClientAddressComesFromTrustedProxiesOnly(t *testing.T) {
	lim := func(o *gateway.Options) {
		o.Limiter = limiter.New(limiter.Options{Now: func() time.Time { return t0 }, InvalidPerMinute: 2})
	}
	forwarded := func(ip string) http.Header {
		h := withBearer(badKey(t))
		h.Set("X-Forwarded-For", ip)
		return h
	}
	t.Run("with the proxy trusted", func(t *testing.T) {
		s, secret := behind(t, newRecording(), lim, func(o *gateway.Options) {
			o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
		})
		for i := 0; i < 2; i++ {
			s.send("GET", "/v1/x", "", forwarded("198.51.100.1"))
		}
		resp, body := s.send("GET", "/v1/x", "", forwarded("198.51.100.1"))
		wantRefusal(t, resp, body, http.StatusTooManyRequests, "rate_limited")
		// Another client behind the same proxy is not throttled with it.
		h := withBearer(secret)
		h.Set("X-Forwarded-For", "198.51.100.2")
		if resp, body := s.send("GET", "/v1/x", "", h); resp.StatusCode != 200 {
			t.Errorf("a second client = %d %s", resp.StatusCode, body)
		}
	})
	t.Run("with no trusted proxy the header changes nothing", func(t *testing.T) {
		s, _ := behind(t, newRecording(), lim)
		for i := 0; i < 2; i++ {
			s.send("GET", "/v1/x", "", forwarded(fmt.Sprintf("198.51.100.%d", i)))
		}
		resp, body := s.send("GET", "/v1/x", "", forwarded("198.51.100.99"))
		wantRefusal(t, resp, body, http.StatusTooManyRequests, "rate_limited")
	})
}

// F6: keman's limiter kept a map entry for every key and every address it ever
// saw, so a flood of made-up ones exhausted its memory.
func TestRegression_F6_TheLimiterStaysBoundedUnderAFloodOfKeysAndAddresses(t *testing.T) {
	lim := limiter.New(limiter.Options{Now: func() time.Time { return t0 }, MaxKeys: 64, MaxAddrs: 64, InvalidPerMinute: 1 << 30})
	s, _ := behind(t, newRecording(), func(o *gateway.Options) {
		o.Limiter = lim
		o.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
		o.Transport = &captureTransport{} // a good key's request goes nowhere real
	})
	// 500 good keys, each with a limit, so each wants a window.
	keys := make([]registry.Key, 500)
	secrets := make([]string, len(keys))
	for i := range keys {
		keys[i], secrets[i] = withPolicy(t, `{"rpm_limit":5}`)
	}
	s.publish(specOfRoute(t, s), keys...)

	flood := func(n int) {
		for i := 0; i < n; i++ {
			// A made-up key from a made-up address, then a good key from another.
			r, _ := http.NewRequest("GET", "/v1/x", nil)
			r.RemoteAddr = "127.0.0.1:1000"
			r.Header.Set("Authorization", "Bearer "+badKey(t))
			r.Header.Set("X-Forwarded-For", fmt.Sprintf("10.%d.%d.%d", byte(i>>16), byte(i>>8), byte(i)))
			s.direct(r)

			r, _ = http.NewRequest("GET", "/v1/x", nil)
			r.RemoteAddr = "127.0.0.1:1000"
			r.Header.Set("Authorization", "Bearer "+secrets[i%len(secrets)])
			r.Header.Set("X-Forwarded-For", fmt.Sprintf("11.%d.%d.%d", byte(i>>16), byte(i>>8), byte(i)))
			s.direct(r)
		}
	}
	flood(2000) // warm up
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	flood(30_000)
	runtime.GC()
	runtime.ReadMemStats(&after)

	keysTracked, addrs := lim.Tracked()
	if keysTracked > 64 || addrs > 64 {
		t.Errorf("tracking %d keys and %d addresses, bounds are 64 and 64", keysTracked, addrs)
	}
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 4<<20 {
		t.Errorf("the heap grew by %d KiB across a flood of 30,000 keys and addresses", grew>>10)
	}
}
