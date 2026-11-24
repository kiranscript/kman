package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/cli"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/store"
)

// withFeed is an install that has a key named my-app, a clock at t0, and these
// events stored: the oldest first.
func withFeed(t *testing.T) (*install, admin.Key) {
	t.Helper()
	in := withOpenAI(t)
	in.now = func() time.Time { return t0 }
	var created admin.CreatedKey
	in.kmanJSON(t, &created, "key", "create", "my-app", "--upstream", "openai")
	k := created.Key
	at := t0.Add(-time.Hour)
	err := in.store.Do(t.Context(), func(tx *store.Tx) error {
		return tx.AddEvents([]store.EventRow{
			{Time: at, Kind: "request", KeyID: k.ID, ClientIP: "203.0.113.7", Country: "DE", ASN: 3320, ASOrg: "Deutsche Telekom\x1b[31m AG", Client: "openai-python/3", Model: "gpt-5.4", Operation: "POST /v1/chat/completions?api_key=sk-leak", Status: 200},
			{Time: at.Add(time.Minute), Kind: "blocked", KeyID: k.ID, ClientIP: "198.51.100.9", Country: "US", Client: "curl/8", Operation: "POST /v1/chat/completions", Status: 403, BlockedCode: "address_not_allowed"},
			{Time: at.Add(2 * time.Minute), Kind: "blocked", ClientIP: "192.0.2.4", Country: "RU", ASN: 12389, Client: "none", Operation: "GET /v1/models", Status: 401, BlockedCode: "invalid_key"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return in, k
}

func TestEventsListsRecentRequestsNewestFirstWithoutTheAddressWhileItIsOff(t *testing.T) {
	in, k := withFeed(t)
	out := in.kman(t, "events")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "TIME") {
		t.Fatalf("kman events:\n%s", out)
	}
	for _, col := range []string{"KEY", "CLIENT", "ADDRESS", "COUNTRY", "ASN", "OPERATION", "MODEL", "STATUS", "CODE"} {
		if !strings.Contains(lines[0], col) {
			t.Errorf("the header lacks %s: %s", col, lines[0])
		}
	}
	probe, refused, forwarded := lines[1], lines[2], lines[3]
	// What called is shown whatever the setting: a client is a program, not a person.
	for line, client := range map[string]string{probe: " none ", refused: " curl/8 ", forwarded: " openai-python/3 "} {
		if !strings.Contains(line, client) {
			t.Errorf("the line lacks the client %q:\n%s", strings.TrimSpace(client), line)
		}
	}
	if !strings.Contains(probe, "invalid_key") || !strings.Contains(probe, "(no key)") || !strings.Contains(probe, "RU") || !strings.Contains(probe, "AS12389") || !strings.Contains(probe, "401") {
		t.Errorf("the request with no key:\n%s", probe)
	}
	if !strings.Contains(refused, "my-app ("+k.ID+")") || !strings.Contains(refused, "address_not_allowed") || !strings.Contains(refused, "2026-11-04T11:01:00Z") {
		t.Errorf("the refusal:\n%s", refused)
	}
	if !strings.Contains(forwarded, "AS3320 Deutsche Telekom [31m AG") || !strings.Contains(forwarded, "gpt-5.4") || !strings.Contains(forwarded, "POST /v1/chat/completions") || !strings.Contains(forwarded, " 200") {
		t.Errorf("the forwarded request:\n%s", forwarded)
	}
	for _, leak := range []string{"203.0.113.7", "198.51.100.9", "192.0.2.4", "api_key", "sk-leak", "\x1b"} {
		if strings.Contains(out, leak) {
			t.Errorf("the output holds %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "withheld") || !strings.Contains(out, "kman settings set expose_client_addresses true") {
		t.Errorf("the output does not say the addresses are withheld and how to see them:\n%s", out)
	}

	in.kman(t, "settings", "set", "expose_client_addresses", "on")
	out = in.kman(t, "events")
	for _, want := range []string{"203.0.113.7", "198.51.100.9", "192.0.2.4"} {
		if !strings.Contains(out, want) {
			t.Errorf("with the setting on the output lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "withheld") {
		t.Errorf("with the setting on the output still says withheld:\n%s", out)
	}
}

func TestEventsFiltersByKeyNameRefusedSinceCodeAndLimit(t *testing.T) {
	in, k := withFeed(t)
	count := func(args ...string) int {
		var page admin.EventPage
		in.kmanJSON(t, &page, append([]string{"events"}, args...)...)
		return len(page.Events)
	}
	for _, c := range []struct {
		args []string
		want int
	}{
		{nil, 3},
		{[]string{"--key", "my-app"}, 2},
		{[]string{"--key", k.ID}, 2},
		{[]string{"--refused"}, 2},
		{[]string{"--key", "my-app", "--refused"}, 1},
		{[]string{"--code", "invalid_key"}, 1},
		{[]string{"--since", "59m"}, 2}, // the oldest is an hour old
		{[]string{"--since", "2d"}, 3},
		{[]string{"--limit", "1"}, 1},
	} {
		if got := count(c.args...); got != c.want {
			t.Errorf("kman events %v --json lists %d events, want %d", c.args, got, c.want)
		}
	}
	var page admin.EventPage
	in.kmanJSON(t, &page, "events", "--limit", "1")
	if !page.More {
		t.Error("--limit 1 says there is no more")
	}
	if out := in.kman(t, "events", "--limit", "1"); !strings.Contains(out, "--limit") {
		t.Errorf("a cut list does not say how to see more:\n%s", out)
	}
	if out := in.kman(t, "events", "--code", "nothing_like_this"); !strings.Contains(out, "No events") {
		t.Errorf("an empty list:\n%s", out)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--since", "yesterday"}, "--since"},
		{[]string{"--key", "nobody"}, "nobody"},
		{[]string{"--limit", "0"}, "--limit"},
		{[]string{"extra"}, "usage"},
	} {
		if r := in.run(t, nil, "", append([]string{"events"}, c.args...)...); r.code != 1 || !strings.Contains(r.stderr, c.want) {
			t.Errorf("kman events %v: exit %d, %s", c.args, r.code, r.stderr)
		}
	}
}

func TestEventsJSONIsTheAPIsPage(t *testing.T) {
	in, k := withFeed(t)
	var page admin.EventPage
	in.kmanJSON(t, &page, "events", "--key", "my-app")
	if len(page.Events) != 2 || page.Events[0].KeyID != k.ID || page.Events[0].KeyName != "my-app" || !page.ClientAddressesWithheld {
		t.Errorf("events --json = %+v", page)
	}
}

// syncBuffer is a buffer that a command writes while a test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// following is kman events --follow, running.
type following struct {
	t              *testing.T
	stdout, stderr *syncBuffer
	cancel         context.CancelFunc
	done           chan struct{} // closed once the command has ended
	code           int
}

func (in *install) follow(t *testing.T, args ...string) *following {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f := &following{t: t, stdout: &syncBuffer{}, stderr: &syncBuffer{}, cancel: cancel, done: make(chan struct{})}
	env := in.env(nil, "", f.stdout, f.stderr)
	go func() {
		f.code = cli.Run(ctx, append([]string{"events", "--follow"}, args...), env)
		close(f.done)
	}()
	t.Cleanup(func() { cancel(); <-f.done })
	return f
}

// waitFor returns once what the command printed holds s.
func (f *following) waitFor(s string) string {
	f.t.Helper()
	start := time.Now()
	for {
		if out := f.stdout.String(); strings.Contains(out, s) {
			return out
		}
		select {
		case <-f.done:
			f.t.Fatalf("kman events --follow ended with exit %d before it printed %q:\n%s%s", f.code, s, f.stdout, f.stderr)
		default:
		}
		if time.Since(start) > 10*time.Second {
			f.t.Fatalf("kman events --follow did not print %q:\n%s%s", s, f.stdout, f.stderr)
		}
		runtime.Gosched()
	}
}

// stop interrupts the command, as Ctrl-C does, and returns its exit code.
func (f *following) stop() int {
	f.cancel()
	<-f.done
	return f.code
}

// withBus makes the install's service stream from a running bus.
func (in *install) withBus(t *testing.T) *events.Bus {
	t.Helper()
	b := events.New(events.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	var done sync.WaitGroup
	done.Go(func() { b.Run(ctx) })
	t.Cleanup(func() { cancel(); done.Wait() })
	in.events = b
	in.upgrade(t, profiles.Presets())
	return b
}

// emitUntilSeen emits e until the follower has printed what, which it does once
// it is subscribed.
func emitUntilSeen(b *events.Bus, f *following, e events.Event, what string) string {
	f.t.Helper()
	start := time.Now()
	for {
		b.Emit(e)
		for range 1000 {
			if out := f.stdout.String(); strings.Contains(out, what) {
				return out
			}
			runtime.Gosched()
		}
		if time.Since(start) > 10*time.Second {
			f.t.Fatalf("kman events --follow did not print %q:\n%s%s", what, f.stdout, f.stderr)
		}
	}
}

func liveEvent(keyID string, kind events.Kind, code, path string) events.Event {
	return events.Event{Time: t0, Kind: kind, KeyID: keyID, ClientIP: netip.MustParseAddr("203.0.113.77"), Country: "FR", ASN: 3215, ASOrg: "Orange S.A.",
		Model: "gpt-5.4", Method: "POST", Path: path, Status: 200, BlockedCode: code}
}

func TestEventsFollowPrintsRecentEventsThenNewOnesUntilInterrupted(t *testing.T) {
	in, k := withFeed(t)
	bus := in.withBus(t)
	f := in.follow(t, "--limit", "2")
	out := f.waitFor("invalid_key")
	// What is already stored comes first, oldest first, so the newest is the
	// last line before the live ones.
	if strings.Index(out, "address_not_allowed") > strings.Index(out, "invalid_key") || strings.Contains(out, "AS3320") {
		t.Errorf("the backfill of 2:\n%s", out)
	}

	out = emitUntilSeen(bus, f, liveEvent(k.ID, events.KindRequest, "", "/v1/embeddings"), "/v1/embeddings")
	if !strings.Contains(out, "my-app ("+k.ID+")") || !strings.Contains(out, "FR") || !strings.Contains(out, "AS3215 Orange S.A.") || strings.Contains(out, "203.0.113.77") {
		t.Errorf("a live event:\n%s", out)
	}
	if code := f.stop(); code != 0 {
		t.Errorf("interrupted: exit %d\n%s", code, f.stderr)
	}
	if !strings.Contains(f.stderr.String(), "expose_client_addresses") {
		t.Errorf("the follower does not say it withholds addresses:\n%s", f.stderr)
	}
}

func TestEventsFollowTakesTheSameFiltersAndSaysWhenAKeyIsNotIdentified(t *testing.T) {
	in, k := withFeed(t)
	bus := in.withBus(t)
	in.kman(t, "settings", "set", "expose_client_addresses", "on")
	f := in.follow(t, "--refused", "--limit", "1", "--key", "my-app")
	f.waitFor("address_not_allowed")
	// A forwarded request and another key's refusal come first and are skipped;
	// the refusal of my-app that follows is the one printed.
	bus.Emit(liveEvent(k.ID, events.KindRequest, "", "/v1/skipped"))
	bus.Emit(liveEvent("", events.KindBlocked, "invalid_key", "/v1/skipped-too"))
	out := emitUntilSeen(bus, f, liveEvent(k.ID, events.KindBlocked, "key_disabled", "/v1/seen"), "/v1/seen")
	if strings.Contains(out, "/v1/skipped") || !strings.Contains(out, "203.0.113.77") {
		t.Errorf("a filtered follow:\n%s", out)
	}

	g := in.follow(t, "--refused", "--limit", "1")
	out = emitUntilSeen(bus, g, liveEvent("", events.KindBlocked, "invalid_key", "/v1/probe"), "/v1/probe")
	if !strings.Contains(out, "(no key)") {
		t.Errorf("a probe is not marked:\n%s", out)
	}
}

func TestEventsFollowJSONPrintsOneEventPerLine(t *testing.T) {
	in, k := withFeed(t)
	bus := in.withBus(t)
	f := in.follow(t, "--json", "--limit", "1")
	f.waitFor(`"refusal_code":"invalid_key"`)
	out := emitUntilSeen(bus, f, liveEvent(k.ID, events.KindRequest, "", "/v1/embeddings"), "/v1/embeddings")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var last admin.Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil || last.Operation != "POST /v1/embeddings" || last.KeyName != "my-app" {
		t.Errorf("last line %q: %v", lines[len(lines)-1], err)
	}
	for _, l := range lines {
		var e admin.Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Errorf("a line that is not an event: %q", l)
		}
	}
}

func TestEventsFollowFailsWhenTheServerHasNoLiveFeed(t *testing.T) {
	in, _ := withFeed(t) // no bus: a process that serves no requests
	r := in.run(t, nil, "", "events", "--follow")
	if r.code != 1 || !strings.Contains(r.stderr, "no live events") {
		t.Errorf("exit %d: %s", r.code, r.stderr)
	}
}

// A server that ends the stream, as kman does when it stops, is an error: the
// operator asked to follow, and is no longer following.
func TestEventsFollowSaysWhenKmanEndsTheStreamAndWhenItDroppedEvents(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/v1/events":
			fmt.Fprint(w, `{"events":[],"more":false}`)
		case "/admin/v1/events/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": kman event stream\n\n")
			fmt.Fprint(w, "data: {\"time\":\"2026-11-04T12:00:00Z\",\"kind\":\"forwarded\",\"key_id\":\"k\",\"operation\":\"GET /v1/models\",\"status\":200}\n\n")
			fmt.Fprint(w, "event: dropped\ndata: {\"missed\":7}\n\n")
		}
	}))
	t.Cleanup(fake.Close)
	if err := cli.SaveCredentials(in.credentials(), fake.URL, in.owner); err != nil {
		t.Fatal(err)
	}
	r := in.run(t, nil, "", "events", "--follow")
	if r.code != 1 || !strings.Contains(r.stdout, "GET /v1/models") || !strings.Contains(r.stderr, "missed 7") || !strings.Contains(r.stderr, "ended") {
		t.Errorf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
}
