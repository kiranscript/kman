package gateway

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/token"
)

// authenticate finds the request's key. A token's checksum is checked before
// anything looks it up, and a key's state is told only to who holds its secret.
func (g *Gateway) authenticate(x *exchange) *refusal {
	tok, err := token.Parse(presentedKey(x.r.Header))
	if err != nil || tok.Kind != token.VirtualKey {
		return &refusal{code: codeInvalidKey}
	}
	route, ok := g.opts.Routes.Lookup(string(tok.ID))
	if !ok {
		return &refusal{code: codeInvalidKey}
	}
	now := g.opts.Now()
	if !secretWorks(route.Key.Secrets, tok.Hash, now) {
		return &refusal{code: codeInvalidKey}
	}
	x.keyID = route.Key.ID // the secret is right, so the key is the one to name
	switch {
	case route.Key.Status == registry.KeyRevoked:
		return &refusal{code: codeKeyRevoked}
	case route.Key.Status == registry.KeyDisabled:
		x.priority = true
		return &refusal{code: codeKeyDisabled}
	case route.Key.Status != registry.KeyActive:
		return &refusal{code: codeInvalidKey} // fail closed on a status this kman doesn't know
	case !route.Key.ExpiresAt.IsZero() && !now.Before(route.Key.ExpiresAt):
		return &refusal{code: codeKeyExpired}
	case route.Credential == nil || route.Upstream == nil:
		x.priority = true                     // a canary, which nothing else tells apart from a bad key
		return &refusal{code: codeInvalidKey} // a canary forwards nothing, and looks like any other bad key
	}
	x.route = route
	return nil
}

// secretWorks reports whether hash is a secret of the key, still in its
// validity. It compares in constant time and looks at every secret, so timing
// tells nothing about which one matched.
func secretWorks(secrets []registry.KeySecret, hash token.Hash, now time.Time) bool {
	found := false
	for i := range secrets {
		s := &secrets[i]
		if s.Hash.Equal(hash) && (s.ValidUntil.IsZero() || now.Before(s.ValidUntil)) {
			found = true
		}
	}
	return found
}

// looksLikeKey reports whether a header value holds a key, alone or as a bearer
// token. It is how the key is found in a provider's own header, and how it is
// kept from going any further.
func looksLikeKey(v string) bool {
	v = withoutBearer(v)
	return token.IsKeyShaped(v)
}

func withoutBearer(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return v
}

// presentedKey finds the virtual key where an app's SDK puts it: in
// Authorization as Bearer, GitHub's token scheme, bare, or Basic with the key as
// the username or the password; in x-api-key; or, for SDKs that send their key in
// a provider's header such as X-Postmark-Server-Token, in any header by its
// prefix. With several, the first in that order wins, and any ties go to the
// lowest header name, so the answer never depends on map order.
func presentedKey(h http.Header) string {
	authorization := fromAuthorization(h.Get("Authorization"))
	if looksLikeKey(authorization) {
		return authorization
	}
	if v := withoutBearer(h.Get("X-Api-Key")); looksLikeKey(v) {
		return v
	}
	lowest, found := "", ""
	for name, values := range h {
		if len(values) > 0 && looksLikeKey(values[0]) && (found == "" || name < lowest) {
			lowest, found = name, withoutBearer(values[0])
		}
	}
	if found != "" {
		return found
	}
	return authorization // whatever it is, for token.Parse to refuse
}

func fromAuthorization(v string) string {
	v = strings.TrimSpace(v)
	scheme, rest, ok := strings.Cut(v, " ")
	if !ok {
		return v
	}
	rest = strings.TrimSpace(rest)
	switch {
	case strings.EqualFold(scheme, "bearer"), strings.EqualFold(scheme, "token"):
		return rest
	case strings.EqualFold(scheme, "basic"):
		raw, err := base64.StdEncoding.DecodeString(rest)
		if err != nil {
			return ""
		}
		user, password, _ := strings.Cut(string(raw), ":")
		if looksLikeKey(password) {
			return password
		}
		return user
	}
	return v
}
