package gateway

import (
	"net/http"
	"net/textproto"
	"strings"
)

// dropped reports whether a header of the app's stops at kman: it is hop by hop,
// it authenticates the app rather than the upstream's credential, or it says
// where a request came from, which no upstream needs. name is in canonical form,
// as net/http parses it.
func dropped(name string) bool {
	switch name {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Expect":
		return true
	case "Authorization", "Api-Key", "X-Api-Key", "X-Goog-Api-Key":
		return true
	case "Forwarded", "Via", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Port",
		"X-Forwarded-Proto", "X-Forwarded-Server", "X-Real-Ip":
		return true
	}
	return false
}

// outboundHeaders is what of the app's headers go on to the upstream. A header
// holding a key, under any name, goes no further than kman. The values are shared
// with in, and nothing changes them.
func outboundHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in)+1)
	for name, values := range in {
		if !dropped(name) && !anyLooksLikeKey(values) {
			out[name] = values
		}
	}
	dropNamedByConnection(out, in["Connection"])
	if _, ok := out["User-Agent"]; !ok {
		out["User-Agent"] = []string{""} // net/http would otherwise send its own
	}
	return out
}

func anyLooksLikeKey(values []string) bool {
	for _, v := range values {
		if looksLikeKey(v) {
			return true
		}
	}
	return false
}

// dropNamedByConnection removes the headers a Connection header says are hop by
// hop.
func dropNamedByConnection(h http.Header, connection []string) {
	for _, line := range connection {
		for name := range strings.SplitSeq(line, ",") {
			delete(h, textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name)))
		}
	}
}

// droppedFromResponse reports whether a header of the upstream's stops at kman:
// it is hop by hop. net/http reframes the response itself.
func droppedFromResponse(name string) bool {
	switch name {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}

func copyResponseHeaders(to, from http.Header) {
	for name, values := range from {
		if !droppedFromResponse(name) {
			to[name] = values
		}
	}
	dropNamedByConnection(to, from["Connection"])
}
