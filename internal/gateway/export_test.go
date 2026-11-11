package gateway

import (
	"net/http"
	"net/netip"
)

// WriteError writes the error for a code, for the test of the error table:
// most codes have no stage that produces them yet.
func WriteError(w http.ResponseWriter, c string) { writeError(w, code(c), "") }

// ClientIP is the address a request is counted as coming from.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr { return clientIP(r, trusted) }
