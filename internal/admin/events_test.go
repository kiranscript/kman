package admin_test

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/store"
)

// feedFixture is an install with a key named my-app and five stored events: a
// forwarded request and a refusal of my-app, the refusal of a request that
// presented no key, a realtime session, and a forwarded request the upstream
// could not answer.
func feedFixture(t *testing.T) (*install, admin.Key) {
	t.Helper()
	in, _ := withCredential(t)
	k := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"}).Key
	at := t0.Add(-time.Hour)
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: at, Kind: "request", KeyID: k.ID, ClientIP: "203.0.113.7", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Client: "openai-python/3", Model: "gpt-5.4", Operation: "POST /v1/chat/completions", Status: 200},
			{Time: at.Add(time.Minute), Kind: "blocked", KeyID: k.ID, ClientIP: "198.51.100.9", Country: "US", Client: "curl/8", Operation: "POST /v1/chat/completions", Status: 403, BlockedCode: "address_not_allowed"},
			{Time: at.Add(2 * time.Minute), Kind: "blocked", ClientIP: "192.0.2.4", Country: "RU", ASN: 12389, Client: "none", Operation: "GET /v1/models", Status: 401, BlockedCode: "invalid_key"},
			{Time: at.Add(3 * time.Minute), Kind: "session", KeyID: k.ID, ClientIP: "203.0.113.8", Country: "DE", ASN: 3320, Model: "gpt-live-1", Operation: "GET /v1/realtime", Status: 101},
			{Time: at.Add(4 * time.Minute), Kind: "request", KeyID: k.ID, Operation: "POST /v1/chat/completions", Status: 502, BlockedCode: "upstream_unavailable"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return in, k
}

func listEvents(t *testing.T, in *install, q admin.EventQuery) admin.EventPage {
	t.Helper()
	page, err := in.svc.Events(as("owner"), q)
	if err != nil {
		t.Fatalf("Events(%+v) = %v", q, err)
	}
	return page
}

func TestEventsListTheNewestFirstWithWhoWhereAndWhatButNotTheAddressUntilItIsTurnedOn(t *testing.T) {
	in, k := feedFixture(t)
	page := listEvents(t, in, admin.EventQuery{})
	if len(page.Events) != 5 || page.More || !page.ClientAddressesWithheld {
		t.Fatalf("page = %+v, want 5 events, no more, and the addresses withheld", page)
	}
	newest, oldest := page.Events[0], page.Events[4]
	if newest.Operation != "POST /v1/chat/completions" || newest.Status != 502 || newest.RefusalCode != "upstream_unavailable" || newest.Kind != "forwarded" || newest.ClientAddressWithheld {
		t.Errorf("newest = %+v: a forwarded event with a code, and no address to withhold", newest)
	}
	if oldest.KeyID != k.ID || oldest.KeyName != "my-app" || oldest.Kind != "forwarded" || oldest.Country != "DE" || oldest.ASN != 3320 || oldest.ASNLabel != "Deutsche Telekom AG" ||
		oldest.Client != "openai-python/3" || oldest.Model != "gpt-5.4" || oldest.Operation != "POST /v1/chat/completions" || oldest.Status != 200 || oldest.ID == 0 || !oldest.Time.Equal(t0.Add(-time.Hour)) {
		t.Errorf("oldest = %+v", oldest)
	}
	// Where a request came from is shown without the address: the country and
	// the AS are, the address is withheld and the event says so.
	if oldest.ClientAddress != "" || !oldest.ClientAddressWithheld {
		t.Errorf("the address of %+v got out with the setting off", oldest)
	}
	for _, e := range page.Events {
		if e.ClientAddress != "" {
			t.Errorf("an event carries the address %q with expose_client_addresses off", e.ClientAddress)
		}
	}
	// A request with no identifiable key is listed, and marked.
	probe := page.Events[2]
	if probe.KeyID != "" || probe.KeyName != "" || !probe.UnidentifiedKey || probe.RefusalCode != "invalid_key" || probe.Kind != "refused" || probe.Country != "RU" || probe.ASN != 12389 || probe.Client != "none" {
		t.Errorf("the event with no key = %+v", probe)
	}
	if page.Events[1].Kind != "forwarded" || page.Events[1].UnidentifiedKey {
		t.Errorf("a realtime session = %+v, want a forwarded event of a known key", page.Events[1])
	}

	in.exposeClients(t, true)
	page = listEvents(t, in, admin.EventQuery{})
	if page.ClientAddressesWithheld {
		t.Error("the page says addresses are withheld with the setting on")
	}
	var addrs []string
	for _, e := range page.Events {
		if e.ClientAddressWithheld {
			t.Errorf("event %+v says its address is withheld with the setting on", e)
		}
		addrs = append(addrs, e.ClientAddress)
	}
	if want := []string{"", "203.0.113.8", "192.0.2.4", "198.51.100.9", "203.0.113.7"}; !slices.Equal(addrs, want) {
		t.Errorf("addresses = %q, want %q", addrs, want)
	}
}

func TestEventsFilterByKeyKindRefusalCodeAndTimeAndPage(t *testing.T) {
	in, k := feedFixture(t)
	at := t0.Add(-time.Hour)
	ops := func(q admin.EventQuery) []string {
		var out []string
		for _, e := range listEvents(t, in, q).Events {
			out = append(out, e.Kind+"/"+e.RefusalCode)
		}
		return out
	}
	for _, c := range []struct {
		name string
		q    admin.EventQuery
		want []string
	}{
		{"a key", admin.EventQuery{Key: k.ID}, []string{"forwarded/upstream_unavailable", "forwarded/", "refused/address_not_allowed", "forwarded/"}},
		{"refused", admin.EventQuery{Kind: "refused"}, []string{"refused/invalid_key", "refused/address_not_allowed"}},
		{"forwarded", admin.EventQuery{Kind: "forwarded"}, []string{"forwarded/upstream_unavailable", "forwarded/", "forwarded/"}},
		{"a code", admin.EventQuery{Code: "invalid_key"}, []string{"refused/invalid_key"}},
		{"since", admin.EventQuery{Since: at.Add(3 * time.Minute)}, []string{"forwarded/upstream_unavailable", "forwarded/"}},
		{"until", admin.EventQuery{Until: at.Add(time.Minute)}, []string{"forwarded/"}},
		{"a key's refusals", admin.EventQuery{Key: k.ID, Kind: "refused"}, []string{"refused/address_not_allowed"}},
	} {
		if got := ops(c.q); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	first := listEvents(t, in, admin.EventQuery{Limit: 2})
	if len(first.Events) != 2 || !first.More || first.NextBefore != first.Events[1].ID {
		t.Fatalf("first page = %+v, want 2 events, more, and a cursor at the last", first)
	}
	second := listEvents(t, in, admin.EventQuery{Limit: 2, Before: first.NextBefore})
	third := listEvents(t, in, admin.EventQuery{Limit: 2, Before: second.NextBefore})
	if len(second.Events) != 2 || !second.More || len(third.Events) != 1 || third.More || third.NextBefore != 0 {
		t.Fatalf("pages = %+v / %+v / %+v", first, second, third)
	}
	ids := map[int64]bool{}
	for _, p := range []admin.EventPage{first, second, third} {
		for _, e := range p.Events {
			ids[e.ID] = true
		}
	}
	if len(ids) != 5 {
		t.Errorf("the pages hold %d distinct events, want 5", len(ids))
	}
}

func TestEventsRefuseWhatTheyCannotAnswer(t *testing.T) {
	in, _ := feedFixture(t)
	for name, q := range map[string]admin.EventQuery{
		"a kind":            {Kind: "blocked"},
		"a negative limit":  {Limit: -1},
		"a huge limit":      {Limit: 1001},
		"a cursor":          {Before: -3},
		"a backwards range": {Since: t0, Until: t0.Add(-time.Hour)},
	} {
		if _, err := in.svc.Events(as("owner"), q); code(err) != admin.CodeInvalid {
			t.Errorf("%s: Events = %v, want an invalid error", name, err)
		}
	}
	if _, err := in.svc.Events(as("owner"), admin.EventQuery{Key: "nokey"}); code(err) != admin.CodeNotFound {
		t.Errorf("an unknown key: Events = %v, want not_found", err)
	}
	if _, err := in.svc.Events(t.Context(), admin.EventQuery{}); code(err) != admin.CodeUnauthorized {
		t.Errorf("with no caller: Events = %v, want unauthorized", err)
	}
}

// A path is the operation, never a query string: a key can ride in one.
func TestAnEventNeverCarriesAPathsQueryString(t *testing.T) {
	in, k := feedFixture(t)
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{{Time: t0, Kind: "request", KeyID: k.ID, Operation: "GET /v1/models?api_key=sk-secret-1234", Status: 200}})
	})
	if err != nil {
		t.Fatal(err)
	}
	e := listEvents(t, in, admin.EventQuery{Limit: 1}).Events[0]
	if e.Operation != "GET /v1/models" {
		t.Errorf("operation = %q, want the path with no query string", e.Operation)
	}
}

// --- the live stream ---

// bus is a running event bus.
func runBus(t *testing.T, opts events.Options) *events.Bus {
	t.Helper()
	b := events.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return b
}

func newEvent(keyID string, kind events.Kind, code string) events.Event {
	return events.Event{Time: t0, Kind: kind, KeyID: keyID, ClientIP: netip.MustParseAddr("203.0.113.7"), Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom AG", Client: "openai-python/3",
		Model: "gpt-5.4", Method: "POST", Path: "/v1/chat/completions", Status: 200, BlockedCode: code}
}

func (in *install) withBus(t *testing.T, opts events.Options) *events.Bus {
	t.Helper()
	in.events = runBus(t, opts)
	in.svc = in.service(profiles.Presets())
	return in.events
}

func openStream(t *testing.T, in *install, q admin.EventStreamQuery) admin.EventStream {
	t.Helper()
	s, err := in.svc.StreamEvents(as("owner"), q)
	if err != nil {
		t.Fatalf("StreamEvents(%+v) = %v", q, err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestTheStreamCarriesNewEventsWithTheKeyNameAndTheSameFilters(t *testing.T) {
	in, k := feedFixture(t)
	bus := in.withBus(t, events.Options{})
	all := openStream(t, in, admin.EventStreamQuery{})
	refused := openStream(t, in, admin.EventStreamQuery{Kind: "refused"})
	mine := openStream(t, in, admin.EventStreamQuery{Key: k.ID, Code: "address_not_allowed"})

	bus.Emit(newEvent(k.ID, events.KindRequest, ""))
	bus.Emit(newEvent(k.ID, events.KindBlocked, "address_not_allowed"))
	bus.Emit(newEvent("", events.KindBlocked, "invalid_key"))

	e := <-all.Events()
	if e.KeyID != k.ID || e.KeyName != "my-app" || e.Kind != "forwarded" || e.Country != "DE" || e.ASN != 3320 || e.ASNLabel != "Deutsche Telekom AG" || e.Client != "openai-python/3" ||
		e.Operation != "POST /v1/chat/completions" || e.Model != "gpt-5.4" || e.Status != 200 || !e.Time.Equal(t0) || e.UnidentifiedKey {
		t.Errorf("first event = %+v", e)
	}
	if e := <-all.Events(); e.Kind != "refused" || e.RefusalCode != "address_not_allowed" || e.KeyName != "my-app" {
		t.Errorf("second event = %+v", e)
	}
	if e := <-all.Events(); e.KeyID != "" || !e.UnidentifiedKey || e.RefusalCode != "invalid_key" || e.KeyName != "" {
		t.Errorf("an event with no key = %+v, want it marked", e)
	}
	// The refused stream skips the forwarded event; the key and code stream
	// skips everything but one.
	if e := <-refused.Events(); e.RefusalCode != "address_not_allowed" {
		t.Errorf("refused stream's first event = %+v", e)
	}
	if e := <-refused.Events(); e.RefusalCode != "invalid_key" {
		t.Errorf("refused stream's second event = %+v", e)
	}
	if e := <-mine.Events(); e.KeyID != k.ID || e.RefusalCode != "address_not_allowed" {
		t.Errorf("filtered stream's event = %+v", e)
	}
}

func TestTheStreamWithholdsTheAddressWhileTheSettingIsOffAndFollowsTheSetting(t *testing.T) {
	in, k := feedFixture(t)
	bus := in.withBus(t, events.Options{})
	s := openStream(t, in, admin.EventStreamQuery{})

	bus.Emit(newEvent(k.ID, events.KindRequest, ""))
	off := <-s.Events()
	if off.ClientAddress != "" || !off.ClientAddressWithheld || off.Country != "DE" || off.ASN != 3320 {
		t.Errorf("with the setting off: %+v, want the country and AS and no address", off)
	}
	in.exposeClients(t, true)
	bus.Emit(newEvent(k.ID, events.KindRequest, ""))
	on := <-s.Events()
	if on.ClientAddress != "203.0.113.7" || on.ClientAddressWithheld {
		t.Errorf("with the setting on: %+v, want the address", on)
	}
	in.exposeClients(t, false)
	bus.Emit(newEvent(k.ID, events.KindRequest, ""))
	if again := <-s.Events(); again.ClientAddress != "" || !again.ClientAddressWithheld {
		t.Errorf("after turning it off again: %+v, want the address withheld", again)
	}
}

func TestAnEventWithNoAddressIsNotSaidToWithholdOne(t *testing.T) {
	in, k := feedFixture(t)
	bus := in.withBus(t, events.Options{})
	s := openStream(t, in, admin.EventStreamQuery{})
	e := newEvent(k.ID, events.KindRequest, "")
	e.ClientIP = netip.Addr{} // a Unix socket
	bus.Emit(e)
	if got := <-s.Events(); got.ClientAddress != "" || got.ClientAddressWithheld {
		t.Errorf("a Unix socket event = %+v", got)
	}
}

func TestAStreamOfAnUnknownKeyOrKindIsRefusedAndOneWithNoBusSaysWhy(t *testing.T) {
	in, _ := feedFixture(t)
	if _, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{}); code(err) != admin.CodeConflict {
		t.Errorf("a stream with no bus = %v, want a conflict", err)
	}
	in.withBus(t, events.Options{})
	if _, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{Kind: "blocked"}); code(err) != admin.CodeInvalid {
		t.Errorf("a bad kind = %v, want invalid", err)
	}
	if _, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{Key: "nokey"}); code(err) != admin.CodeNotFound {
		t.Errorf("an unknown key = %v, want not_found", err)
	}
	if _, err := in.svc.StreamEvents(t.Context(), admin.EventStreamQuery{}); code(err) != admin.CodeUnauthorized {
		t.Errorf("with no caller = %v, want unauthorized", err)
	}
	if n := len(in.events.Subscriptions()); n != 0 {
		t.Errorf("%d subscriptions remain after refused streams", n)
	}
}

func TestAClosedStreamReleasesItsSubscription(t *testing.T) {
	in, k := feedFixture(t)
	bus := in.withBus(t, events.Options{})
	s, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(bus.Subscriptions()); n != 1 {
		t.Fatalf("%d subscriptions with a stream open, want 1", n)
	}
	s.Close()
	s.Close() // twice is harmless
	for range s.Events() {
	} // the channel closes once the stream has let go
	if n := len(bus.Subscriptions()); n != 0 {
		t.Errorf("%d subscriptions after Close, want none", n)
	}
	bus.Emit(newEvent(k.ID, events.KindRequest, "")) // nobody is listening: nothing breaks
}

func TestAStreamEndsWhenItsContextDoes(t *testing.T) {
	in, _ := feedFixture(t)
	bus := in.withBus(t, events.Options{})
	ctx, cancel := context.WithCancel(as("owner"))
	s, err := in.svc.StreamEvents(ctx, admin.EventStreamQuery{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for range s.Events() {
	}
	if n := len(bus.Subscriptions()); n != 0 {
		t.Errorf("%d subscriptions after the request ended, want none", n)
	}
}

func TestStoppingTheStreamsEndsEveryOpenOneAndRefusesNewOnes(t *testing.T) {
	in, _ := feedFixture(t)
	in.withBus(t, events.Options{})
	a := openStream(t, in, admin.EventStreamQuery{})
	b := openStream(t, in, admin.EventStreamQuery{Kind: "refused"})
	in.svc.StopEventStreams()
	for range a.Events() {
	}
	for range b.Events() {
	}
	if _, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{}); code(err) != admin.CodeConflict {
		t.Errorf("a stream after the streams were stopped = %v, want a conflict", err)
	}
	if n := len(in.events.Subscriptions()); n != 0 {
		t.Errorf("%d subscriptions remain", n)
	}
}

func TestAStreamEndsWhenTheBusStops(t *testing.T) {
	in, _ := feedFixture(t)
	b := events.New(events.Options{})
	in.events = b
	in.svc = in.service(profiles.Presets())
	s := openStream(t, in, admin.EventStreamQuery{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	cancel()
	<-done
	for range s.Events() {
	}
}

func TestTooManyOpenStreamsAreRefusedUntilOneCloses(t *testing.T) {
	in, _ := feedFixture(t)
	in.withBus(t, events.Options{})
	var open []admin.EventStream
	for {
		s, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{})
		if err != nil {
			if code(err) != admin.CodeConflict || !strings.Contains(err.Error(), "open") {
				t.Fatalf("stream %d = %v, want a conflict about open streams", len(open)+1, err)
			}
			break
		}
		open = append(open, s)
		if len(open) > 1000 {
			t.Fatal("no limit on open streams")
		}
	}
	if len(open) < 2 {
		t.Fatalf("only %d streams allowed", len(open))
	}
	open[0].Close()
	for range open[0].Events() {
	}
	s, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{})
	if err != nil {
		t.Fatalf("a stream after one closed = %v", err)
	}
	s.Close()
	for _, s := range open {
		s.Close()
	}
}

// A client that does not read never holds up the request path, and what it
// missed is counted, in its stream and in the bus's drops by subscriber.
func TestASlowStreamLosesEventsNotTheRequestPathAndTheDropsAreCounted(t *testing.T) {
	in, k := feedFixture(t)
	in.streamBuffer = 2
	synctest.Test(t, func(t *testing.T) {
		bus := events.New(events.Options{Capacity: 64})
		in.events = bus
		in.svc = in.service(profiles.Presets())
		ctx, cancel := context.WithCancel(context.Background())
		var done sync.WaitGroup
		done.Go(func() { bus.Run(ctx) })
		s, err := in.svc.StreamEvents(as("owner"), admin.EventStreamQuery{})
		if err != nil {
			t.Fatal(err)
		}
		for range 30 { // none of this blocks, whatever the client does
			bus.Emit(newEvent(k.ID, events.KindRequest, ""))
		}
		synctest.Wait()
		missed := s.Missed()
		if missed == 0 || missed >= 30 {
			t.Fatalf("missed %d of 30 events, want some but not all", missed)
		}
		// Everything that was not missed is still there to read.
		for range 30 - missed {
			<-s.Events()
		}
		s.Close()
		if n := bus.SubscriberDrops()["event-stream"]; n != missed {
			t.Errorf("the bus counted %d drops for event-stream, the stream %d", n, missed)
		}
		cancel()
		done.Wait()
	})
}
