package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// DefaultMaxBody is how much of a request body kman holds in memory, which is
// more than any JSON request an upstream's own limits let through.
const DefaultMaxBody = 32 << 20

// The bounds on reading request bodies (the security review, finding S-1). A
// body is read before anything is forwarded, so without them a key holder could
// keep a connection, and up to DefaultMaxBody of memory, for as long as it
// liked, on as many connections as it liked, and a key's rate limit would not
// notice, since it counts requests that have been read.
const (
	// DefaultBodyStall is how long a body may go without a byte arriving.
	DefaultBodyStall = 30 * time.Second
	// DefaultBodyTimeout is how long a body may take whole, however steadily it
	// arrives: DefaultMaxBody at a tenth of a megabyte a second.
	DefaultBodyTimeout = 5 * time.Minute
	// DefaultMaxBuffered is the most body bytes held across all requests: sixteen
	// bodies of the largest size.
	DefaultMaxBuffered = 16 * DefaultMaxBody
)

// initialBuffer limits what a stated length can make kman allocate before any of
// the body has arrived.
const initialBuffer = 1 << 20

// errBufferFull ends a body that would take kman past MaxBuffered.
var errBufferFull = errors.New("gateway: request bodies in memory are at their limit")

// buffer reads the request body into memory, up to the limit, because a later
// stage must read the model from it before anything is forwarded. It holds the
// read to the bounds above, and counts what it holds until the request ends.
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
	guard := guards.Get().(*bodyGuard)
	guard.start(g, x, http.MaxBytesReader(x.w, r.Body, limit))
	_, err := buf.ReadFrom(guard)
	fired := guard.stop()
	if err != nil {
		switch {
		case isMaxBytes(err):
			return &refusal{code: codeRequestTooLarge}
		case errors.Is(err, errBufferFull):
			return &refusal{code: codeRateLimited, retryAfter: time.Second,
				message: "kman is holding as many request bodies as it can. Try again in a moment."}
		case fired:
			g.opts.Log.Warn("a request body stopped arriving, or took too long, so the connection was closed", "key", x.route.Key.ID)
		}
		x.abort = true // the app went away, or was cut off, part way through its body
		return &refusal{}
	}
	x.body = buf.Bytes()
	return nil
}

func isMaxBytes(err error) bool {
	_, ok := errors.AsType[*http.MaxBytesError](err)
	return ok
}

// bodyGuard reads one request's body under the bounds above. It counts what it
// reads against the gateway's bound on memory, and ends the connection if the
// body stalls or takes too long, by moving the connection's read deadline into
// the past, which fails the read. Guards are pooled, timers included, so a
// request pays nothing for one.
type bodyGuard struct {
	g *Gateway
	x *exchange
	r io.Reader

	stall, total *time.Timer
	every        time.Duration

	mu    sync.Mutex // fire against stop
	set   deadliner
	done  bool
	fired bool
}

var guards = sync.Pool{New: func() any {
	b := &bodyGuard{}
	b.stall = time.AfterFunc(time.Hour, b.fire)
	b.total = time.AfterFunc(time.Hour, b.fire)
	b.stall.Stop()
	b.total.Stop()
	return b
}}

// deadliner is what net/http's response writers have, and a ResponseController
// would find.
type deadliner interface{ SetReadDeadline(time.Time) error }

func (b *bodyGuard) start(g *Gateway, x *exchange, r io.Reader) {
	b.g, b.x, b.r, b.every = g, x, r, g.opts.BodyStall
	b.set, _ = x.w.(deadliner) // nil for a writer that can't, such as a test's recorder: no time bound
	b.mu.Lock()
	b.done, b.fired = false, false
	b.mu.Unlock()
	if b.set != nil {
		b.stall.Reset(b.every)
		b.total.Reset(g.opts.BodyTimeout)
	}
}

func (b *bodyGuard) fire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.done && b.set != nil {
		b.fired = true
		b.set.SetReadDeadline(time.Unix(1, 0))
	}
}

// Read reads the body, telling the timers that bytes are still arriving.
func (b *bodyGuard) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if n > 0 {
		if b.set != nil {
			b.stall.Reset(b.every)
		}
		b.x.held += int64(n)
		if b.g.buffered.Add(int64(n)) > b.g.opts.MaxBuffered {
			return 0, errBufferFull // what it counted is given back when the request ends
		}
	}
	return n, err
}

// stop ends the guard, reporting whether it fired, and returns the connection's
// read deadline to none, unless it fired: it outlives this request, and the next
// one would meet it. A guard whose timer was already running is not pooled again, since its
// function could still be waiting to run against the next request.
func (b *bodyGuard) stop() (fired bool) {
	stalled, late := b.stall.Stop(), b.total.Stop()
	quiet := b.set == nil || (stalled && late) // timers never armed have nothing to wait for
	b.mu.Lock()
	b.done = true
	fired = b.fired
	if !fired && b.set != nil {
		// Once it has fired the deadline stays in the past, which is what keeps net/http
		// from waiting on the rest of the body when it closes the connection.
		b.set.SetReadDeadline(time.Time{})
	}
	b.mu.Unlock()
	b.g, b.x, b.r, b.set = nil, nil, nil, nil
	if quiet {
		guards.Put(b)
	}
	return fired
}
