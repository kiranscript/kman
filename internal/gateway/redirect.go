package gateway

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kirank55/kman/internal/registry"
)

// keepOnKman applies the redirect rule to a response: kman follows no redirect,
// and an app that follows one must stay on kman, or it would take its virtual key
// to wherever the upstream points. It rewrites a redirect within the upstream to a
// path on kman, and reports false for a redirect anywhere else, which the app must
// not see.
func keepOnKman(resp *http.Response, up *registry.Upstream) bool {
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return true
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return true // such as 304, which redirects nowhere
	}
	target, ok := withinUpstream(location, up)
	if ok {
		resp.Header.Set("Location", target)
	}
	return ok
}

// withinUpstream returns what a Location becomes on kman, and false if it leads
// off the upstream or can't be read for certain. Clients read backslashes and
// repeated slashes more loosely than url.Parse, so those are refused outright.
func withinUpstream(location string, up *registry.Upstream) (string, bool) {
	if strings.Contains(location, `\`) || strings.HasPrefix(location, "//") {
		return "", false
	}
	u, err := url.Parse(location)
	if err != nil || u.Opaque != "" || u.User != nil {
		return "", false
	}
	if u.Scheme == "" && u.Host == "" {
		return location, true // relative already
	}
	if !sameOrigin(u, up) {
		return "", false
	}
	target := u.RequestURI()
	if strings.HasPrefix(target, "//") {
		return "", false // with the host gone, this would name one
	}
	if u.Fragment != "" {
		target += "#" + u.EscapedFragment()
	}
	return target, true
}

func sameOrigin(u *url.URL, up *registry.Upstream) bool {
	if u.Scheme != scheme(up) {
		return false
	}
	if !strings.EqualFold(u.Hostname(), up.Host) && (up.ServerName == "" || !strings.EqualFold(u.Hostname(), up.ServerName)) {
		return false
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if up.TLS {
			port = "443"
		}
	}
	return port == strconv.Itoa(up.Port)
}
