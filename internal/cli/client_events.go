package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

func (c *client) Events(ctx context.Context, req admin.EventQuery) (admin.EventPage, error) {
	q := url.Values{}
	for name, v := range map[string]string{"key": req.Key, "kind": req.Kind, "code": req.Code} {
		if v != "" {
			q.Set(name, v)
		}
	}
	if !req.Since.IsZero() {
		q.Set("since", req.Since.UTC().Format(time.RFC3339Nano))
	}
	if !req.Until.IsZero() {
		q.Set("until", req.Until.UTC().Format(time.RFC3339Nano))
	}
	if req.Before != 0 {
		q.Set("before", strconv.FormatInt(req.Before, 10))
	}
	if req.Limit != 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	var out admin.EventPage
	return out, c.do(ctx, "GET", "/events", q, nil, &out)
}

// maxStreamMessage bounds one server-sent message: an event is a few hundred
// bytes.
const maxStreamMessage = 1 << 20

// StreamEvents follows the live feed over /events/stream, as server-sent events,
// until ctx ends, the stream is closed, or kman ends it.
func (c *client) StreamEvents(ctx context.Context, req admin.EventStreamQuery) (admin.EventStream, error) {
	q := url.Values{}
	for name, v := range map[string]string{"key": req.Key, "kind": req.Kind, "code": req.Code} {
		if v != "" {
			q.Set(name, v)
		}
	}
	u := c.base + prefix + "/events/stream"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	ctx, cancel := context.WithCancel(ctx)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Accept", "text/event-stream")
	// The client's timeout covers a whole response, which a stream outlasts.
	resp, err := (&http.Client{Transport: c.hc.Transport}).Do(httpReq)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cannot reach kman at %s (is kman serve running?): %w", c.shown, unwrapURLError(err))
	}
	if resp.StatusCode >= 300 {
		defer cancel()
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, c.responseError(resp.Status, raw)
	}
	st := &httpStream{out: make(chan admin.Event), cancel: cancel, body: resp.Body, shown: c.shown}
	go st.read(ctx)
	return st, nil
}

// httpStream is a live feed read from the admin API.
type httpStream struct {
	out    chan admin.Event
	cancel context.CancelFunc
	body   io.ReadCloser
	shown  string
	missed atomic.Int64
	err    atomic.Pointer[error]
}

func (s *httpStream) Events() <-chan admin.Event { return s.out }
func (s *httpStream) Missed() int64              { return s.missed.Load() }
func (s *httpStream) Close()                     { s.cancel() }

func (s *httpStream) Err() error {
	if e := s.err.Load(); e != nil {
		return *e
	}
	return nil
}

// read parses the server-sent messages until the response ends. An event goes to
// out, a dropped message sets Missed, and a comment is a keep-alive.
func (s *httpStream) read(ctx context.Context) {
	defer close(s.out)
	defer s.body.Close()
	sc := bufio.NewScanner(s.body)
	sc.Buffer(make([]byte, 0, 64<<10), maxStreamMessage)
	var event, data string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			name, payload := event, data
			event, data = "", ""
			if payload == "" {
				continue
			}
			switch name {
			case "":
				var e admin.Event
				if err := json.Unmarshal([]byte(payload), &e); err != nil {
					s.fail(fmt.Errorf("%s sent what is not an event: %w", s.shown, err))
					return
				}
				select {
				case s.out <- e:
				case <-ctx.Done():
					return
				}
			case "dropped":
				var d struct {
					Missed int64 `json:"missed"`
				}
				if json.Unmarshal([]byte(payload), &d) == nil {
					s.missed.Store(d.Missed)
				}
			}
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimPrefix(line[len("data:"):], " ")
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) {
		s.fail(fmt.Errorf("the stream from %s broke: %w", s.shown, unwrapURLError(err)))
	}
}

func (s *httpStream) fail(err error) { s.err.Store(&err) }
