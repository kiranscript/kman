package profiles

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/kirank55/kman/internal/registry"
)

// Profile is what the gateway needs from one kind of upstream.
type Profile interface {
	// Authorize puts a credential on an outbound request's headers, replacing
	// anything already under that name. It keeps no reference to secret, which
	// the vault wipes in place.
	Authorize(h http.Header, a registry.Auth, secret []byte)
}

// For returns the profile with this name. Until metering brings the OpenAI and
// Anthropic profiles (#40), all of them are the generic one, which forwards
// without reading what passes through.
func For(string) Profile { return generic{} }

// generic is the generic HTTP profile (SPEC §6): header or Basic auth.
type generic struct{}

func (generic) Authorize(h http.Header, a registry.Auth, secret []byte) {
	if a.Style == registry.AuthBasic {
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(basicPair(a.BasicUsername, secret)))
		return
	}
	// Spec.Validate holds the template to "Name: value {key}".
	name, value, _ := strings.Cut(a.Header, ":")
	h.Set(strings.TrimSpace(name), strings.Replace(strings.TrimSpace(value), registry.KeyPlaceholder, string(secret), 1))
}

// basicPair is the user:password of Basic auth. With no username, the secret is
// the whole pair.
func basicPair(username string, secret []byte) []byte {
	if username == "" {
		return secret
	}
	pair := make([]byte, 0, len(username)+1+len(secret))
	pair = append(pair, username...)
	pair = append(pair, ':')
	return append(pair, secret...)
}
