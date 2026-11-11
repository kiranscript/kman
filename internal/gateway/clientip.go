package gateway

import (
	"net/http"
	"net/netip"
	"strings"
)

// clientIP is the address a request is counted as coming from: the connection's,
// or, when the connection is from a trusted proxy, the client that proxy
// reports in X-Forwarded-For. It is the zero Addr when there is none, as on a
// Unix socket.
//
// The header is read from its right, which is the end each proxy appends to: the
// client's address is the first one, going left, that no trusted proxy of ours
// put there. Anything to the left of it is what the client claimed, and is
// ignored. If every address is a trusted proxy's, the leftmost is the client
// (an internal one). An entry that isn't an address ends the walk at the
// proxy's own address, which is safe: it throttles all of that proxy's clients
// as one.
func clientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := parseConn(r.RemoteAddr)
	if !peer.IsValid() || !isTrusted(trusted, peer) {
		return peer
	}
	var entries []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for e := range strings.SplitSeq(v, ",") {
			entries = append(entries, strings.TrimSpace(e))
		}
	}
	client := peer
	for i := len(entries) - 1; i >= 0; i-- {
		a := parseAddr(entries[i])
		if !a.IsValid() {
			return peer
		}
		client = a
		if !isTrusted(trusted, a) {
			break
		}
	}
	return client
}

func isTrusted(trusted []netip.Prefix, a netip.Addr) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseConn reads a connection's RemoteAddr: "ip:port", with nothing for a Unix
// socket. It allocates nothing for a good one, which is every request's.
func parseConn(s string) netip.Addr {
	if s == "" {
		return netip.Addr{}
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return clean(ap.Addr())
	}
	return parseAddr(s)
}

// parseAddr reads an address with or without a port, in the forms
// X-Forwarded-For holds one: 1.2.3.4, 1.2.3.4:80, ::1, [::1]:80.
func parseAddr(s string) netip.Addr {
	if a, err := netip.ParseAddr(s); err == nil {
		return clean(a)
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return clean(ap.Addr())
	}
	return netip.Addr{}
}

func clean(a netip.Addr) netip.Addr { return a.Unmap().WithZone("") }
