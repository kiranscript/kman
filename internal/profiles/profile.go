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
	// Prepare readies a request so that its response can be metered, setting h and
	// returning the body to send, which is the one given if nothing changes. It
	// doesn't change the body given. path is the request's. injectUsage is the
	// upstream's setting.
	Prepare(h http.Header, path string, body []byte, injectUsage bool) []byte
	// UsageReader returns what reads usage out of a response with these headers
	// as it passes through, or nil when kman can't read it or doesn't meter it.
	UsageReader(h http.Header) *UsageReader
	// Meters reports whether this profile's upstreams are metered: every request
	// has spend, capped or not. Generic is not.
	Meters() bool
	// Ask reads what a request body says about what it will cost. It has no
	// Metered request for a profile that doesn't meter.
	Ask(body []byte) Asked
	// Free reports whether a route costs nothing and reports no usage, so a
	// response with none isn't charged as a request that ran.
	Free(path string) bool
}

// For returns the profile with this name, which is an upstream's Profile and not
// its Name or Preset. An unknown name is the generic profile.
func For(name string) Profile {
	switch name {
	case registry.ProfileOpenAI:
		return openAI{}
	case registry.ProfileAnthropic:
		return anthropic{}
	}
	return generic{}
}

// generic is the generic HTTP profile (SPEC §6): header or Basic auth. It is
// unmetered: it forwards without reading what passes through.
type generic struct{}

func (generic) Prepare(_ http.Header, _ string, body []byte, _ bool) []byte { return body }

func (generic) UsageReader(http.Header) *UsageReader { return nil }

func (generic) Meters() bool { return false }

func (generic) Ask([]byte) Asked { return Asked{} }

func (generic) Free(string) bool { return false }

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
