package profiles_test

import (
	"net/http"
	"testing"

	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

func TestTheGenericProfilePresentsTheCredentialTheWayTheUpstreamAsks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		auth         registry.Auth
		secret       string
		header, want string
	}{
		{"a bearer header", registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"}, "sk-real", "Authorization", "Bearer sk-real"},
		{"a bare header", registry.Auth{Style: registry.AuthHeader, Header: "Authorization: {key}"}, "lin-real", "Authorization", "lin-real"},
		{"a lower-case name", registry.Auth{Style: registry.AuthHeader, Header: "x-api-key: {key}"}, "sk-ant", "X-Api-Key", "sk-ant"},
		{"text after the key", registry.Auth{Style: registry.AuthHeader, Header: "X-Thing: a {key} b"}, "s", "X-Thing", "a s b"},
		// Expected values from: printf 'api:s3cret' | base64
		{"basic with a username", registry.Auth{Style: registry.AuthBasic, BasicUsername: "api"}, "s3cret", "Authorization", "Basic YXBpOnMzY3JldA=="},
		// With no username, the secret is the whole user:password.
		{"basic with no username", registry.Auth{Style: registry.AuthBasic}, "AC123:tok", "Authorization", "Basic QUMxMjM6dG9r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			profiles.For(registry.ProfileGeneric).Authorize(h, tc.auth, []byte(tc.secret))
			if got := h.Values(tc.header); len(got) != 1 || got[0] != tc.want {
				t.Errorf("%s = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// The credential replaces whatever the header held: it must never go out beside
// a value the app sent.
func TestTheCredentialReplacesAValueAlreadyInTheHeader(t *testing.T) {
	h := http.Header{"Authorization": {"Bearer from-the-app"}}
	profiles.For(registry.ProfileGeneric).Authorize(h, registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"}, []byte("sk-real"))
	if got := h.Values("Authorization"); len(got) != 1 || got[0] != "Bearer sk-real" {
		t.Errorf("Authorization = %q", got)
	}
}

// The header it writes must not alias the vault's copy, which a rotation wipes
// in place.
func TestTheHeaderDoesNotShareMemoryWithTheSecret(t *testing.T) {
	secret := []byte("sk-real")
	h := http.Header{}
	profiles.For(registry.ProfileGeneric).Authorize(h, registry.Auth{Style: registry.AuthHeader, Header: "Authorization: {key}"}, secret)
	clear(secret)
	if got := h.Get("Authorization"); got != "sk-real" {
		t.Errorf("after the secret was wiped, the header holds %q", got)
	}
}

// Until the OpenAI and Anthropic profiles arrive with metering (#40), every
// profile presents credentials as the generic one does.
func TestEveryProfilePresentsACredential(t *testing.T) {
	for _, name := range []string{registry.ProfileOpenAI, registry.ProfileAnthropic, registry.ProfileGeneric} {
		h := http.Header{}
		profiles.For(name).Authorize(h, registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"}, []byte("k"))
		if got := h.Get("Authorization"); got != "Bearer k" {
			t.Errorf("profile %s: Authorization = %q", name, got)
		}
	}
}
