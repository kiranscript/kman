package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
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
	watch := g.watchBody(x.w)
	defer watch.stop()
	_, err := buf.ReadFrom(&heldReader{r: http.MaxBytesReader(x.w, r.Body, limit), g: g, x: x, watch: watch})
	if err != nil {
		switch {
		case isMaxBytes(err):
			return &refusal{code: codeRequestTooLarge}
		case errors.Is(err, errBufferFull):
			return &refusal{code: codeRateLimited, retryAfter: time.Second,
				message: "kman is holding as many request bodies as it can. Try again in a moment."}
		case watch.fired.Load():
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

// heldReader reads a body, counting what it has read against the gateway's
// bound on memory, and telling the watch that bytes are still arriving.
type heldReader struct {
	r     io.Reader
	g     *Gateway
	x     *exchange
	watch *bodyWatch
}

func (h *heldReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	if n > 0 {
		h.watch.progress()
		h.x.held += int64(n)
		if h.g.buffered.Add(int64(n)) > h.g.opts.MaxBuffered {
			return 0, errBufferFull // what it counted is given back when the request ends
		}
	}
	return n, err
}

// bodyWatch ends a request's connection if its body stalls or takes too long, by
// moving the connection's read deadline into the past, which fails the read.
type bodyWatch struct {
	rc           *http.ResponseController
	stall, total *time.Timer
	every        time.Duration
	fired        atomic.Bool
}

// watchBody starts the bounds on one request body. A server that can't set read
// deadlines, such as a test's recorder, gets no bound.
func (g *Gateway) watchBody(w http.ResponseWriter) *bodyWatch {
	b := &bodyWatch{rc: http.NewResponseController(w), every: g.opts.BodyStall}
	fire := func() {
		b.fired.Store(true)
		b.rc.SetReadDeadline(time.Unix(1, 0))
	}
	b.stall = time.AfterFunc(b.every, fire)
	b.total = time.AfterFunc(g.opts.BodyTimeout, fire)
	return b
}

func (b *bodyWatch) progress() { b.stall.Reset(b.every) }

// stop ends the watch and, if it did not fire, returns the connection's read
// deadline to none: it outlives this request, and the next one would meet it.
func (b *bodyWatch) stop() {
	b.stall.Stop()
	b.total.Stop()
	if !b.fired.Load() {
		b.rc.SetReadDeadline(time.Time{})
	}
}
