package gateway_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/token"
)

// openAIError is the shape OpenAI's SDKs read an error from.
type openAIError struct {
	Error struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Param   *string `json:"param"`
		Code    string  `json:"code"`
	} `json:"error"`
}

// wantRefusal checks a response is the error SPEC §6 gives for this code.
func wantRefusal(t *testing.T, resp *http.Response, body string, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("status = %d, want %d: %s", resp.StatusCode, status, body)
	}
	if got := resp.Header.Get("X-Kman-Error"); got != code {
		t.Errorf("X-Kman-Error = %q, want %q", got, code)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var e openAIError
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("the body is not OpenAI-shaped: %v: %s", err, body)
	}
	if e.Error.Code != code || e.Error.Message == "" || e.Error.Type == "" {
		t.Errorf("error = %+v, want code %q with a message and a type", e.Error, code)
	}
}

func TestARequestReachesTheUpstreamWithTheCredentialInPlaceOfTheKey(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)

	resp, body := s.send("POST", "/v1/things", `{"a":1}`, withBearer(secret))

	if resp.StatusCode != 200 || body != `{"ok":true}` {
		t.Errorf("response = %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if v := resp.Header.Get("X-Kman-Error"); v != "" {
		t.Errorf("X-Kman-Error = %q on a success", v)
	}
	got := up.next(t)
	if got.method != "POST" || got.uri != "/v1/things" || got.body != `{"a":1}` {
		t.Errorf("the upstream got %s %s %q", got.method, got.uri, got.body)
	}
	if a := got.header.Values("Authorization"); len(a) != 1 || a[0] != "Bearer "+credential {
		t.Errorf("Authorization = %q", a)
	}
}

// tamper changes the checksum, so the token no longer parses.
func tamper(secret string) string {
	last := "0"
	if strings.HasSuffix(secret, "0") {
		last = "1"
	}
	return secret[:len(secret)-1] + last
}

// A bad checksum is rejected offline: a flood of made-up keys never reaches the
// registry.
func TestABadChecksumIsRefusedWithoutTouchingTheRegistry(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)
	before := s.routes.lookups.Load()

	resp, body := s.send("GET", "/v1/things", "", withBearer(tamper(secret)))

	wantRefusal(t, resp, body, 401, "invalid_key")
	if n := s.routes.lookups.Load() - before; n != 0 {
		t.Errorf("the registry was asked %d times", n)
	}
	up.none(t)
}

func TestWhatIsNotAKeyOfThisInstallIsAnInvalidKey(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)
	stranger, err := token.MintVirtualKey(token.NewKeyID())
	if err != nil {
		t.Fatal(err)
	}
	real, err := token.Parse(secret)
	if err != nil {
		t.Fatal(err)
	}
	sameIDOtherSecret, err := token.MintVirtualKey(real.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := token.MintChildKey(real.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, header := range map[string]http.Header{
		"no key at all":              nil,
		"a real upstream key":        withBearer("sk-not-a-kman-key"),
		"an empty bearer":            withBearer(""),
		"an unknown key id":          withBearer(stranger.Reveal()),
		"the right id, wrong secret": withBearer(sameIDOtherSecret.Reveal()),
		"an admin token":             withBearer(token.MintAdminToken().Reveal()),
		"a child key, for now":       withBearer(child.Reveal()),
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := s.send("GET", "/v1/things", "", header)
			wantRefusal(t, resp, body, 401, "invalid_key")
		})
	}
	up.none(t)
}

func TestAKeysStateRefusesIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*registry.Key)
		status int
		code   string
	}{
		{"revoked", func(k *registry.Key) { k.Status = "revoked" }, 401, "key_revoked"},
		{"disabled", func(k *registry.Key) { k.Status = "disabled" }, 403, "key_disabled"},
		{"expired", func(k *registry.Key) { k.ExpiresAt = t0 }, 401, "key_expired"},
		{"expired and revoked", func(k *registry.Key) { k.ExpiresAt = t0; k.Status = "revoked" }, 401, "key_revoked"},
		{"expired and disabled", func(k *registry.Key) { k.ExpiresAt = t0; k.Status = "disabled" }, 403, "key_disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newRecording()
			s, _ := behind(t, up)
			k, secret := newKey(t)
			tc.edit(&k)
			s.publish(specOfRoute(t, s), k)
			resp, body := s.send("GET", "/v1/things", "", withBearer(secret))
			wantRefusal(t, resp, body, tc.status, tc.code)
			up.none(t)
		})
	}
}

// A key's state is not told to whoever lacks its secret.
func TestTheStateOfAKeyIsNotToldWithoutItsSecret(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up)
	k, secret := newKey(t)
	k.Status = "revoked"
	s.publish(specOfRoute(t, s), k)
	parsed, err := token.Parse(secret)
	if err != nil {
		t.Fatal(err)
	}
	other, err := token.MintVirtualKey(parsed.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp, body := s.send("GET", "/", "", withBearer(other.Reveal()))
	wantRefusal(t, resp, body, 401, "invalid_key")
}

func TestAKeyWorksUntilTheMomentItExpires(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up)
	k, secret := newKey(t)
	k.ExpiresAt = t0.Add(time.Millisecond)
	s.publish(specOfRoute(t, s), k)
	if resp, body := s.send("GET", "/", "", withBearer(secret)); resp.StatusCode != 200 {
		t.Errorf("before its expiry: %d %s", resp.StatusCode, body)
	}
	s.now = t0.Add(time.Millisecond)
	resp, body := s.send("GET", "/", "", withBearer(secret))
	wantRefusal(t, resp, body, 401, "key_expired")
}

// A previous secret works for its grace period and not a moment after.
func TestASecretWorksUntilItsValidityEnds(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up)
	k, secret := newKey(t)
	old, oldSecret := newKeyWithID(t, k.ID)
	k.Secrets = append(k.Secrets, registry.KeySecret{Hash: old, ValidUntil: t0.Add(time.Minute)})
	s.publish(specOfRoute(t, s), k)

	if resp, body := s.send("GET", "/", "", withBearer(oldSecret)); resp.StatusCode != 200 {
		t.Errorf("in its grace, the previous secret got %d %s", resp.StatusCode, body)
	}
	s.now = t0.Add(time.Minute)
	resp, body := s.send("GET", "/", "", withBearer(oldSecret))
	wantRefusal(t, resp, body, 401, "invalid_key")
	if resp, body := s.send("GET", "/", "", withBearer(secret)); resp.StatusCode != 200 {
		t.Errorf("the current secret got %d %s", resp.StatusCode, body)
	}
}

// A canary has no credential, so it can never be forwarded.
func TestAKeyWithNoCredentialForwardsNothing(t *testing.T) {
	up := newRecording()
	s, _ := behind(t, up)
	k, secret := newKey(t)
	k.CredentialID = ""
	s.publish(specOfRoute(t, s), k)
	resp, body := s.send("GET", "/", "", withBearer(secret))
	wantRefusal(t, resp, body, 401, "invalid_key")
	up.none(t)
}

func TestACredentialTheVaultDoesNotHoldIsAnUpstreamFailure(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up)
	s.vault.Delete(credID)
	resp, body := s.send("GET", "/", "", withBearer(secret))
	wantRefusal(t, resp, body, 502, "upstream_unavailable")
	up.none(t)
}

// The key arrives wherever an app's SDK puts it, and never goes further.
func TestAKeyIsAcceptedWhereAppsSendIt(t *testing.T) {
	basic := func(pair string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(pair)) }
	for _, tc := range []struct {
		name   string
		header func(secret string) http.Header
	}{
		{"a bearer token", withBearer},
		{"a lower-case scheme", func(k string) http.Header { return http.Header{"Authorization": {"bearer " + k}} }},
		{"the token scheme", func(k string) http.Header { return http.Header{"Authorization": {"token " + k}} }},
		{"a bare Authorization", func(k string) http.Header { return http.Header{"Authorization": {k}} }},
		{"Basic, as the username", func(k string) http.Header { return http.Header{"Authorization": {basic(k + ":")}} }},
		{"Basic, as the password", func(k string) http.Header { return http.Header{"Authorization": {basic("AC123:" + k)}} }},
		{"x-api-key", func(k string) http.Header { return http.Header{"X-Api-Key": {k}} }},
		{"a provider's own header", func(k string) http.Header { return http.Header{"X-Postmark-Server-Token": {k}} }},
		{"a provider's own header with a bearer", func(k string) http.Header { return http.Header{"X-Thing": {"Bearer " + k}} }},
		{"x-api-key beside a useless Authorization", func(k string) http.Header {
			return http.Header{"Authorization": {"Bearer sk-from-the-sdk"}, "X-Api-Key": {k}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newRecording()
			s, secret := behind(t, up)
			resp, body := s.send("GET", "/", "", tc.header(secret))
			if resp.StatusCode != 200 {
				t.Fatalf("response = %d %s", resp.StatusCode, body)
			}
			got := up.next(t)
			for name, values := range got.header {
				for _, v := range values {
					if strings.Contains(v, secret) {
						t.Errorf("%s reached the upstream holding the key", name)
					}
				}
			}
			if a := got.header.Get("Authorization"); a != "Bearer "+credential {
				t.Errorf("Authorization = %q", a)
			}
		})
	}
}

// The error table, from SPEC §6. The later stages' codes are in it already, so
// each stage only has to name its code.
func TestEveryCodeHasTheStatusTheSpecGivesIt(t *testing.T) {
	for code, status := range map[string]int{
		"invalid_key":          401,
		"key_expired":          401,
		"key_revoked":          401,
		"key_disabled":         403,
		"path_not_allowed":     403,
		"model_not_allowed":    403,
		"address_not_allowed":  403,
		"unmetered_request":    403,
		"unknown_model_price":  403,
		"spend_cap_reached":    403,
		"request_too_large":    413,
		"rate_limited":         429,
		"upstream_unavailable": 502,
		"store_unavailable":    503,
	} {
		t.Run(code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			gateway.WriteError(rec, code)
			resp := rec.Result()
			wantRefusal(t, resp, rec.Body.String(), status, code)
		})
	}
}
