package events

import (
	"net/netip"
	"time"
)

// Kind is what an event records.
type Kind string

const (
	// KindRequest is a request kman forwarded to an upstream.
	KindRequest Kind = "request"
	// KindBlocked is a request kman refused before it reached an upstream, or
	// that the upstream was never reached for.
	KindBlocked Kind = "blocked"
	// KindSession is a realtime session, which produces one event when it ends.
	KindSession Kind = "session"
)

// Event is the record of one request or one realtime session (SPEC §9). It
// holds what happened and never what was said: no request or response content,
// no header, no query string, and no secret.
type Event struct {
	Time time.Time
	Kind Kind
	// KeyID is the virtual key's id, or empty when the request presented no key
	// that kman could identify.
	KeyID    string
	ClientIP netip.Addr // the zero Addr for a Unix socket
	Country  string     // ISO 3166-1 alpha-2, empty until a GeoIP database is configured
	ASN      int64      // 0 when unknown
	// UpstreamID and Model say where the request went and what it asked for.
	UpstreamID, Model string
	// Method and Path are the operation: the request's method and path, with no
	// query string.
	Method, Path string
	// Status is the status the app was sent, 0 when none was.
	Status int
	// BlockedCode is the error code of a refusal, such as key_disabled.
	BlockedCode string
	Latency     time.Duration
	ReqBytes    int64
	RespBytes   int64

	TokensIn, TokensOut           int64
	TokensCached                  int64
	TokensCacheWrite              int64
	TokensAudioIn, TokensAudioOut int64

	// CostMicro is the cost in microdollars when CostKnown. An event whose cost
	// is unknown carries none.
	CostMicro     int64
	CostKnown     bool
	CostEstimated bool // the cost is the reservation, charged because the real one is unknown

	// Priority sends the event down the priority lane: it is for a disabled or
	// canary key, whose use is alerted on without waiting behind ordinary events.
	Priority bool
}

// Operation is the request's method and path, such as "POST /v1/messages".
func (e Event) Operation() string {
	if e.Method == "" && e.Path == "" {
		return ""
	}
	return e.Method + " " + e.Path
}
