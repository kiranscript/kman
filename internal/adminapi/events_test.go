package adminapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiranscript/kman/internal/admin"
	"github.com/kiranscript/kman/internal/adminapi"
	"github.com/kiranscript/kman/internal/events"
	"github.com/kiranscript/kman/internal/store"
)

func TestTheAPIListsStoredEventsNewestFirstAndBindsTheFilters(t *testing.T) {
	s := newServer(t)
	at := t0.Add(-time.Hour)
	err := s.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: at, Kind: "request", ClientIP: "203.0.113.7", Country: "DE", ASN: 3320, Operation: "POST /v1/chat/completions", Status: 200},
			{Time: at.Add(time.Minute), Kind: "blocked", ClientIP: "198.51.100.9", Status: 401, BlockedCode: "invalid_key", Operation: "GET /v1/models"},
			{Time: at.Add(2 * time.Minute), Kind: "blocked", Status: 403, BlockedCode: "key_disabled"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	var page admin.EventPage
	s.do(t, s.owner, "GET", "/admin/v1/events", "").decode(t, 200, &page)
	if len(page.Events) != 3 || page.Events[0].RefusalCode != "key_disabled" || page.Events[2].Country != "DE" || !page.ClientAddressesWithheld {
		t.Fatalf("GET /events = %+v", page)
	}

	q := url.Values{"kind": {"refused"}, "code": {"invalid_key"}, "since": {at.Format(time.RFC3339)}, "until": {at.Add(time.Hour).Format(time.RFC3339)}, "limit": {"1"}}
	s.do(t, s.owner, "GET", "/admin/v1/events?"+q.Encode(), "").decode(t, 200, &page)
	if len(page.Events) != 1 || page.Events[0].Operation != "GET /v1/models" || page.More {
		t.Errorf("GET /events?%s = %+v", q.Encode(), page)
	}
	s.do(t, s.owner, "GET", "/admin/v1/events?limit=1", "").decode(t, 200, &page)
	if !page.More || page.NextBefore == 0 {
		t.Fatalf("a page of one = %+v, want more and a cursor", page)
	}
	var older admin.EventPage
	s.do(t, s.owner, "GET", "/admin/v1/events?limit=1&before="+url.QueryEscape(jsonInt(page.NextBefore)), "").decode(t, 200, &older)
	if len(older.Events) != 1 || older.Events[0].RefusalCode != "invalid_key" {
		t.Errorf("the next page = %+v", older)
	}

	for name, query := range map[string]string{
		"a time that is not RFC 3339":  "since=yesterday",
		"a limit that is not a number": "limit=many",
		"a kind that is not one":       "kind=blocked",
	} {
		if r := s.do(t, s.owner, "GET", "/admin/v1/events?"+query, ""); r.status != http.StatusBadRequest || r.errorCode(t) != "invalid" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := s.do(t, s.owner, "GET", "/admin/v1/events?key=nokey", ""); r.status != http.StatusNotFound {
		t.Errorf("an unknown key: %d %s", r.status, r.body)
	}
	if r := s.do(t, "", "GET", "/admin/v1/events", ""); r.status != http.StatusUnauthorized {
		t.Errorf("with no token: %d", r.status)
	}
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// feed is a stream of server-sent events, read as they arrive.
type feed struct {
	resp *http.Response
	msgs chan message
}

// message is one server-sent message, or a comment.
type message struct{ event, data, comment string }

// follow opens GET /events/stream and reads its messages until the test ends.
func follow(t *testing.T, s *server, query string) *feed {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", s.url+"/admin/v1/events/stream"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.owner)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	f := &feed{resp: resp, msgs: make(chan message, 100)}
	t.Cleanup(func() { resp.Body.Close() })
	go func() {
		defer close(f.msgs)
		sc := bufio.NewScanner(resp.Body)
		var m message
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if m != (message{}) {
					f.msgs <- m
				}
				m = message{}
			case strings.HasPrefix(line, ":"):
				f.msgs <- message{comment: strings.TrimSpace(line[1:])}
			case strings.HasPrefix(line, "event: "):
				m.event = line[len("event: "):]
			case strings.HasPrefix(line, "data: "):
				m.data = line[len("data: "):]
			}
		}
	}()
	return f
}

// next is the next message that is not a comment.
func (f *feed) next(t *testing.T) message {
	t.Helper()
	for m := range f.msgs {
		if m.comment == "" {
			return m
		}
	}
	t.Fatal("the stream ended")
	return message{}
}

func busServer(t *testing.T) (*server, *events.Bus) {
	t.Helper()
	bus := events.New(events.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	var done sync.WaitGroup
	done.Go(func() { bus.Run(ctx) })
	t.Cleanup(func() { cancel(); done.Wait() })
	return newServerWith(t, func(o *admin.Options) { o.Events = bus }), bus
}

func TestTheStreamIsServerSentEventsOfNewEventsWithTheSameFilters(t *testing.T) {
	s, bus := busServer(t)
	f := follow(t, s, "?kind=refused")
	if f.resp.StatusCode != 200 || !strings.HasPrefix(f.resp.Header.Get("Content-Type"), "text/event-stream") || f.resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, Content-Type %q, Cache-Control %q", f.resp.StatusCode, f.resp.Header.Get("Content-Type"), f.resp.Header.Get("Cache-Control"))
	}
	bus.Emit(events.Event{Time: t0, Kind: events.KindRequest, Method: "GET", Path: "/v1/models", Status: 200})
	bus.Emit(events.Event{Time: t0, Kind: events.KindBlocked, ClientIP: netip.MustParseAddr("192.0.2.4"), Country: "RU", ASN: 12389, ASOrg: "Rostelecom",
		Method: "GET", Path: "/v1/models", Status: 401, BlockedCode: "invalid_key"})

	m := f.next(t)
	var e admin.Event
	if err := json.Unmarshal([]byte(m.data), &e); err != nil || m.event != "" {
		t.Fatalf("message %+v: %v", m, err)
	}
	if e.Kind != "refused" || e.RefusalCode != "invalid_key" || !e.UnidentifiedKey || e.Country != "RU" || e.ASNLabel != "Rostelecom" || e.Operation != "GET /v1/models" {
		t.Errorf("event = %+v, want the refusal and not the forwarded request", e)
	}
	// The address is the setting's to give out.
	if e.ClientAddress != "" || !e.ClientAddressWithheld || strings.Contains(m.data, "192.0.2.4") {
		t.Errorf("the stream gave out an address with the setting off: %s", m.data)
	}
}

func TestTheStreamNeedsAnAdminTokenAndChecksItsFiltersBeforeItStarts(t *testing.T) {
	s, _ := busServer(t)
	if r := s.do(t, "", "GET", "/admin/v1/events/stream", ""); r.status != http.StatusUnauthorized {
		t.Errorf("with no token: %d", r.status)
	}
	r := s.do(t, s.owner, "GET", "/admin/v1/events/stream?kind=blocked", "")
	if r.status != http.StatusBadRequest || r.errorCode(t) != "invalid" || !strings.HasPrefix(r.header.Get("Content-Type"), "application/json") {
		t.Errorf("a bad kind: %d %s", r.status, r.body)
	}
	if r := s.do(t, s.owner, "GET", "/admin/v1/events/stream?key=nokey", ""); r.status != http.StatusNotFound {
		t.Errorf("an unknown key: %d %s", r.status, r.body)
	}
	plain := newServer(t) // no bus: serves no requests
	if r := plain.do(t, plain.owner, "GET", "/admin/v1/events/stream", ""); r.status != http.StatusConflict {
		t.Errorf("with no event bus: %d %s", r.status, r.body)
	}
}

func TestTheStreamSaysItIsStillThere(t *testing.T) {
	defer adminapi.SetKeepAlive(5 * time.Millisecond)()
	s, _ := busServer(t)
	f := follow(t, s, "")
	for range 3 {
		if m := <-f.msgs; m.comment == "" || m.event != "" || m.data != "" {
			t.Fatalf("message %+v, want a comment", m)
		}
	}
}

func TestAClientThatLeavesFreesItsSubscriptionAndShutdownEndsTheStreams(t *testing.T) {
	s, bus := busServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", s.url+"/admin/v1/events/stream", nil)
	req.Header.Set("Authorization", "Bearer "+s.owner)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(bus.Subscriptions()); n != 1 {
		t.Fatalf("%d subscriptions with a client connected, want 1", n)
	}
	cancel()
	resp.Body.Close()
	for len(bus.Subscriptions()) > 0 {
		runtime.Gosched()
	}

	f := follow(t, s, "")
	s.svc.StopEventStreams()
	for range f.msgs { // the server ends the response
	}
}

// slowStream is a stream that says it missed events.
type slowStream struct {
	admin.EventStream
	c chan admin.Event
}

func (s slowStream) Events() <-chan admin.Event { return s.c }
func (slowStream) Missed() int64                { return 7 }
func (slowStream) Err() error                   { return nil }
func (slowStream) Close()                       {}

type slowService struct {
	adminapi.Service
	c chan admin.Event
}

func (s slowService) StreamEvents(context.Context, admin.EventStreamQuery) (admin.EventStream, error) {
	return slowStream{c: s.c}, nil
}

func TestAStreamTellsItsClientHowManyEventsItMissed(t *testing.T) {
	s := newServer(t)
	c := make(chan admin.Event)
	srv := newHTTPServer(t, slowService{Service: s.svc, c: c})
	s.url = srv
	f := follow(t, s, "")
	c <- admin.Event{Kind: "forwarded", KeyID: "k"}
	if m := f.next(t); m.event != "" || !strings.Contains(m.data, `"key_id":"k"`) {
		t.Fatalf("first message %+v", m)
	}
	if m := f.next(t); m.event != "dropped" || m.data != `{"missed":7}` {
		t.Errorf("second message %+v, want the count of what was missed", m)
	}
	close(c)
	for range f.msgs {
	}
}
