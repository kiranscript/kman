package gateway

import (
	"bytes"
	"errors"
	"net/http"
)

// DefaultMaxBody is how much of a request body kman holds in memory, which is
// more than any JSON request an upstream's own limits let through.
const DefaultMaxBody = 32 << 20

// initialBuffer limits what a stated length can make kman allocate before any of
// the body has arrived.
const initialBuffer = 1 << 20

// buffer reads the request body into memory, up to the limit, because a later
// stage must read the model from it before anything is forwarded.
func (g *Gateway) buffer(x *exchange) *refusal {
	r := x.r
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	limit := g.opts.MaxBody
	// A stated length over the limit needs no reading: a limit of 0 trips on the
	// first byte. MaxBytesReader, not a check here, because it also has the server
	// close the connection in the way that lets the app read this answer.
	if r.ContentLength > limit {
		limit = 0
	}
	var buf bytes.Buffer
	if n := r.ContentLength; n > 0 {
		buf.Grow(int(min(n, initialBuffer)) + bytes.MinRead)
	}
	if _, err := buf.ReadFrom(http.MaxBytesReader(x.w, r.Body, limit)); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return &refusal{code: codeRequestTooLarge}
		}
		x.abort = true // the app went away part way through its body
		return &refusal{}
	}
	x.body = buf.Bytes()
	return nil
}
