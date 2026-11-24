package admin

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

// The kinds of event the feed tells apart: what kman forwarded to an upstream
// (and realtime sessions), and what it refused.
const (
	EventForwarded = "forwarded"
	EventRefused   = "refused"
)

// Event is one request or realtime session kman saw, as the live feed shows it
// (SPEC §9): who made it, where from, what it asked for and what came of it.
// It holds no request or response content, no header, no query string and no
// secret.
type Event struct {
	ID                    int64     `json:"id,omitempty" doc:"The stored event's id, which pages by. Not set on an event from the live stream, which is not stored yet."`
	Time                  time.Time `json:"time"`
	Kind                  string    `json:"kind" doc:"forwarded: kman sent the request to an upstream, or it was a realtime session. refused: kman answered it itself, with a refusal code."`
	KeyID                 string    `json:"key_id,omitempty" doc:"The virtual key's id. Empty when the request presented no key that kman could identify."`
	KeyName               string    `json:"key_name,omitempty"`
	UnidentifiedKey       bool      `json:"unidentified_key,omitempty" doc:"The request presented no key that kman could identify: a key that does not exist, or none. Many of these from one place is probing."`
	ClientAddress         string    `json:"client_address,omitempty" doc:"The client address: the connection's, or X-Forwarded-For only through trusted_proxies. Left out unless the expose_client_addresses setting is on."`
	ClientAddressWithheld bool      `json:"client_address_withheld,omitempty" doc:"The event has a client address, which is left out because expose_client_addresses is off."`
	Country               string    `json:"country,omitempty" doc:"ISO 3166-1 code, from the country database, when there is one that knows the address."`
	ASN                   int64     `json:"asn,omitempty" doc:"The autonomous system number, from the ASN database, when there is one that knows the address."`
	ASNLabel              string    `json:"asn_label,omitempty" doc:"The autonomous system's organisation, when the ASN database names one."`
	Client                string    `json:"client,omitempty" doc:"What made the request: its User-Agent reduced to a product and major version, such as openai-python/3 or curl/8, or none when it sent no User-Agent. Never the header itself. Empty for an event stored before kman recorded it. It is not personal data, so it is always shown."`
	Operation             string    `json:"operation,omitempty" doc:"The request's method and path, such as POST /v1/chat/completions. Never a query string."`
	Model                 string    `json:"model,omitempty"`
	Status                int       `json:"status,omitempty" doc:"The status the app was sent."`
	RefusalCode           string    `json:"refusal_code,omitempty" doc:"The error code of a refusal, such as key_disabled or invalid_key."`
}

// EventQuery picks a page of stored events. They always come newest first.
type EventQuery struct {
	Key    string    `json:"-" query:"key" doc:"Only events of this key id."`
	Kind   string    `json:"-" query:"kind" doc:"Only events of this kind: forwarded or refused."`
	Code   string    `json:"-" query:"code" doc:"Only events with this refusal code, such as invalid_key."`
	Since  time.Time `json:"-" query:"since" doc:"Only events at or after this time (RFC 3339)."`
	Until  time.Time `json:"-" query:"until" doc:"Only events before this time (RFC 3339)."`
	Before int64     `json:"-" query:"before" doc:"Return the events older than the one with this id: the previous page's next_before. From the newest when 0."`
	Limit  int       `json:"-" query:"limit" doc:"The most events to return, 1 to 1000. Default 100."`
}

// EventPage is a page of stored events, newest first.
type EventPage struct {
	Events []Event `json:"events"`
	More   bool    `json:"more" doc:"Whether there are older events that match."`
	// NextBefore is the cursor for the next page.
	NextBefore int64 `json:"next_before,omitempty" doc:"The id to send as before for the next, older page. Set when more is true."`
	// ClientAddressesWithheld says a client address was left out of an event: a
	// client address is personal data, so the admin API gives it out only with
	// expose_client_addresses on (ADR 0010).
	ClientAddressesWithheld bool `json:"client_addresses_withheld,omitempty" doc:"Client addresses were left out because expose_client_addresses is off."`
}

// EventStreamQuery picks the events of a live stream: the filters of EventQuery
// that say which events, with none of those that say which page.
type EventStreamQuery struct {
	Key  string `json:"-" query:"key" doc:"Only events of this key id."`
	Kind string `json:"-" query:"kind" doc:"Only events of this kind: forwarded or refused."`
	Code string `json:"-" query:"code" doc:"Only events with this refusal code, such as invalid_key."`
}

// EventStream is a live feed of new events. Read them from Events until it is
// closed; Close lets go of it.
type EventStream interface {
	// Events carries the events as they happen. It is closed when the stream
	// ends, whoever ended it: Close, the context, or kman stopping.
	Events() <-chan Event
	// Missed is how many matching events this stream lost because the reader was
	// behind. A reader that does not keep up loses events; it never holds up the
	// request path.
	Missed() int64
	// Err says why the stream ended, once Events is closed: nil when it was
	// closed on purpose or kman stopped.
	Err() error
	Close()
}

const (
	defaultEventLimit = 100
	maxEventLimit     = 1000

	// defaultStreamBuffer is how many events a stream holds for a client that is
	// behind, before it starts losing them.
	defaultStreamBuffer = 256
	// maxEventStreams bounds the streams open at once: each holds a subscription
	// and a connection.
	maxEventStreams = 16
	// streamBatch is how many waiting events a stream reads the settings and key
	// names for in one go.
	streamBatch = 64
	// streamName names the stream's subscriptions on the bus, so their drops
	// are counted together (kman_event_subscriber_dropped_total).
	streamName = "event-stream"
)

// eventKinds turns the kind a caller asked for into the kinds of event it means.
func eventKinds(kind string) ([]string, error) {
	switch kind {
	case "":
		return nil, nil
	case EventForwarded:
		return []string{string(events.KindRequest), string(events.KindSession)}, nil
	case EventRefused:
		return []string{string(events.KindBlocked)}, nil
	}
	return nil, errorf(CodeInvalid, "kind %q is not one of forwarded or refused", kind)
}

// withoutQuery cuts a query string from an operation. An event is meant never to
// hold one, since a key can ride in it; this is the second lock on that door.
func withoutQuery(op string) string {
	if i := strings.IndexByte(op, '?'); i >= 0 {
		return op[:i]
	}
	return op
}

// newEvent builds the feed's event. Its client address is left out unless
// expose, and the event says so when it has one that was.
func newEvent(kind, keyID, keyName, ip string, expose bool) Event {
	e := Event{Kind: EventForwarded, KeyID: keyID, KeyName: keyName, UnidentifiedKey: keyID == ""}
	if kind == string(events.KindBlocked) {
		e.Kind = EventRefused
	}
	switch {
	case ip == "":
	case expose:
		e.ClientAddress = ip
	default:
		e.ClientAddressWithheld = true
	}
	return e
}

// Events lists stored events, newest first. Where a request came from is
// shown as its country and AS; the client address itself only when the
// install turned expose_client_addresses on. It reads and changes nothing.
func (s *Service) Events(ctx context.Context, q EventQuery) (EventPage, error) {
	kinds, err := eventKinds(q.Kind)
	if err != nil {
		return EventPage{}, err
	}
	switch {
	case q.Limit < 0 || q.Limit > maxEventLimit:
		return EventPage{}, errorf(CodeInvalid, "limit must be from 1 to %d", maxEventLimit)
	case q.Before < 0:
		return EventPage{}, errorf(CodeInvalid, "before must not be negative")
	case !q.Since.IsZero() && !q.Until.IsZero() && !q.Until.After(q.Since):
		return EventPage{}, errorf(CodeInvalid, "until must be after since")
	}
	if q.Limit == 0 {
		q.Limit = defaultEventLimit
	}
	out := EventPage{Events: []Event{}}
	err = s.read(ctx, func(tx *store.Tx) error {
		settings, err := loadSettings(tx)
		if err != nil {
			return err
		}
		if q.Key != "" {
			if _, err := tx.Key(q.Key); err != nil {
				return fromStore(err, "key "+q.Key)
			}
		}
		rows, err := tx.ListEvents(store.EventFilter{KeyID: q.Key, Kinds: kinds, BlockedCode: q.Code, Since: q.Since, Until: q.Until, BeforeID: q.Before}, q.Limit+1)
		if err != nil {
			return err
		}
		if len(rows) > q.Limit {
			rows, out.More = rows[:q.Limit], true
		}
		for _, r := range rows {
			e := newEvent(r.Kind, r.KeyID, r.KeyName, r.ClientIP, settings.ExposeClientAddresses)
			e.ID, e.Time, e.Country, e.ASN, e.ASNLabel, e.Client = r.ID, r.Time, r.Country, r.ASN, r.ASOrg, r.Client
			e.Operation, e.Model, e.Status, e.RefusalCode = withoutQuery(r.Operation), r.Model, r.Status, r.BlockedCode
			out.ClientAddressesWithheld = out.ClientAddressesWithheld || e.ClientAddressWithheld
			out.Events = append(out.Events, e)
		}
		if out.More {
			out.NextBefore = rows[len(rows)-1].ID
		}
		return nil
	})
	return out, err
}

// streams is the live streams a Service has open.
type streams struct {
	mu      sync.Mutex
	open    int
	stopped bool
	ctx     context.Context // ends when the streams are stopped
	stop    context.CancelFunc
}

func newStreams() *streams {
	ctx, stop := context.WithCancel(context.Background())
	return &streams{ctx: ctx, stop: stop}
}

// admit counts a new stream, unless there are too many or they were stopped.
func (st *streams) admit() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case st.stopped:
		return errorf(CodeConflict, "kman is shutting down")
	case st.open >= maxEventStreams:
		return errorf(CodeConflict, "%d event streams are open already, which is the most; close one first", maxEventStreams)
	}
	st.open++
	return nil
}

func (st *streams) release() {
	st.mu.Lock()
	st.open--
	st.mu.Unlock()
}

// StopEventStreams ends every open live stream and refuses new ones. kman serve
// calls it when it starts to shut down, so that a client that follows the feed
// does not keep the listener from closing.
func (s *Service) StopEventStreams() {
	s.streams.mu.Lock()
	s.streams.stopped = true
	s.streams.mu.Unlock()
	s.streams.stop()
}

// streamFilter says which events a stream carries.
type streamFilter struct {
	key, code string
	refused   *bool
}

func (f streamFilter) matches(e events.Event) bool {
	return (f.key == "" || e.KeyID == f.key) &&
		(f.code == "" || e.BlockedCode == f.code) &&
		(f.refused == nil || *f.refused == (e.Kind == events.KindBlocked))
}

// StreamEvents opens a live feed of the events that happen from now on, with
// the filters of Events. It subscribes to the event bus, which never waits for a
// subscriber: a client that cannot keep up loses events, counted in its stream's
// Missed and in kman_event_subscriber_dropped_total, and the request path is
// never held up. Its events carry client addresses only while the setting is on,
// read again for each batch.
func (s *Service) StreamEvents(ctx context.Context, q EventStreamQuery) (EventStream, error) {
	if _, err := caller(ctx); err != nil {
		return nil, err
	}
	kinds, err := eventKinds(q.Kind)
	if err != nil {
		return nil, err
	}
	if s.bus == nil {
		return nil, errorf(CodeConflict, "this kman process serves no requests, so it has no live events")
	}
	if q.Key != "" {
		if err := s.read(ctx, func(tx *store.Tx) error {
			_, err := tx.Key(q.Key)
			return fromStore(err, "key "+q.Key)
		}); err != nil {
			return nil, err
		}
	}
	f := streamFilter{key: q.Key, code: q.Code}
	if len(kinds) > 0 {
		refused := q.Kind == EventRefused
		f.refused = &refused
	}
	if err := s.streams.admit(); err != nil {
		return nil, err
	}
	buffer := s.streamBuffer
	if buffer <= 0 {
		buffer = defaultStreamBuffer
	}
	ctx, cancel := context.WithCancel(ctx)
	st := &serviceStream{out: make(chan Event), sub: s.bus.Subscribe(streamName, events.SubOptions{Buffer: buffer}), cancel: cancel}
	go func() {
		// Closing out comes last: a reader that sees it close can open a new stream.
		defer close(st.out)
		defer s.streams.release()
		defer s.bus.Unsubscribe(st.sub)
		s.pump(ctx, st, f)
	}()
	return st, nil
}

// serviceStream is StreamEvents' stream, in process.
type serviceStream struct {
	out    chan Event
	sub    *events.Subscription
	cancel context.CancelFunc
}

func (st *serviceStream) Events() <-chan Event { return st.out }
func (st *serviceStream) Missed() int64        { return st.sub.Dropped() }
func (st *serviceStream) Err() error           { return nil }
func (st *serviceStream) Close()               { st.cancel() }

// pump turns what the subscription delivers into the stream's events, until the
// stream is closed or the bus stops. Only it ever waits for the reader, and
// what the bus has for a reader that waits too long it drops.
func (s *Service) pump(ctx context.Context, st *serviceStream, f streamFilter) {
	for {
		var batch []events.Event
		select {
		case e, ok := <-st.sub.C:
			if !ok {
				return
			}
			if f.matches(e) {
				batch = append(batch, e)
			}
		case <-ctx.Done():
			return
		case <-s.streams.ctx.Done():
			return
		}
		// Whatever else is waiting is turned into events with one read of the
		// settings and the key names.
	drain:
		for len(batch) < streamBatch {
			select {
			case e, ok := <-st.sub.C:
				if !ok {
					break drain
				}
				if f.matches(e) {
					batch = append(batch, e)
				}
			default:
				break drain
			}
		}
		if len(batch) == 0 {
			continue
		}
		for _, e := range s.streamEvents(ctx, batch) {
			select {
			case st.out <- e:
			case <-ctx.Done():
				return
			case <-s.streams.ctx.Done():
				return
			}
		}
	}
}

// streamEvents describes a batch of bus events. If the settings cannot be read,
// the addresses are withheld: a failure never gives one out.
func (s *Service) streamEvents(ctx context.Context, batch []events.Event) []Event {
	expose := false
	names := map[string]string{}
	_ = s.store.Do(ctx, func(tx *store.Tx) error {
		settings, err := loadSettings(tx)
		if err != nil {
			return err
		}
		expose = settings.ExposeClientAddresses
		for _, e := range batch {
			if _, done := names[e.KeyID]; done || e.KeyID == "" {
				continue
			}
			names[e.KeyID] = ""
			if k, err := tx.Key(e.KeyID); err == nil {
				names[e.KeyID] = k.Name
			}
		}
		return nil
	})
	out := make([]Event, len(batch))
	for i, b := range batch {
		var ip string
		if b.ClientIP.IsValid() {
			ip = b.ClientIP.String()
		}
		e := newEvent(string(b.Kind), b.KeyID, names[b.KeyID], ip, expose)
		e.Time, e.Country, e.ASN, e.ASNLabel, e.Client = b.Time, b.Country, b.ASN, b.ASOrg, b.Client
		e.Operation, e.Model, e.Status, e.RefusalCode = withoutQuery(b.Operation()), b.Model, b.Status, b.BlockedCode
		out[i] = e
	}
	return out
}
