package gateway

import (
	"net/http"
	"net/netip"

	"github.com/kirank55/kman/internal/policy/geoip"
)

// WriteError writes the error for a code, for the test of the error table:
// most codes have no stage that produces them yet.
func WriteError(w http.ResponseWriter, c string) { writeError(w, code(c), "") }

// ClientIP is the address a request is counted as coming from.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr { return clientIP(r, trusted) }

// Buffered is how many bytes of request bodies the gateway holds now.
func (g *Gateway) Buffered() int64 { return g.buffered.Load() }

// ObserveEmit has the gateway tell f each request's address and place as the
// request's event is emitted.
func ObserveEmit(o *Options, f func(netip.Addr, geoip.Info)) { o.observe = f }
