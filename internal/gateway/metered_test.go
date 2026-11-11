package gateway_test

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

// reports collects what the gateway read from each response.
type reports struct {
	mu  sync.Mutex
	got []profiles.Usage
	key []string
}

func (r *reports) on(o *gateway.Options) {
	o.OnUsage = func(route registry.Route, u profiles.Usage) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.got = append(r.got, u)
		r.key = append(r.key, route.Key.ID)
	}
}

func (r *reports) none(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) != 0 {
		t.Errorf("usage reports: %+v", r.got)
	}
}

func (r *reports) only(t *testing.T) profiles.Usage {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) != 1 {
		t.Fatalf("%d usage reports: %+v", len(r.got), r.got)
	}
	return r.got[0]
}

// meteredBehind is a gateway in front of handler, with an upstream on this
// profile.
func meteredBehind(t *testing.T, handler http.Handler, profile string, inject bool, r *reports) (*stack, string, string) {
	t.Helper()
	s, _ := behind(t, handler, r.on)
	spec := specOfRoute(t, s)
	spec.Profile, spec.InjectUsage = profile, inject
	k, secret := newKey(t)
	s.publish(spec, k)
	return s, secret, k.ID
}

func answering(contentType, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		io.WriteString(w, body)
	})
}

func TestTheGatewayReadsUsageFromAResponseItPassesOnUnchanged(t *testing.T) {
	const body = `{"model":"gpt-x","usage":{"prompt_tokens":5,"completion_tokens":2}}`
	r := &reports{}
	s, secret, id := meteredBehind(t, answering("application/json", body), registry.ProfileOpenAI, true, r)
	resp, got := s.send("POST", "/v1/chat/completions", `{"model":"gpt-x"}`, withBearer(secret))
	if resp.StatusCode != 200 || got != body {
		t.Errorf("response = %d %q", resp.StatusCode, got)
	}
	want := profiles.Usage{Seen: true, Model: "gpt-x", InputTokens: 5, OutputTokens: 2}
	if u := r.only(t); !reflect.DeepEqual(u, want) {
		t.Errorf("usage = %+v", u)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.key[0] != id {
		t.Errorf("key = %q, want %q", r.key[0], id)
	}
}

// SPEC §8: usage reported is charged whatever the status.
func TestUsageIsReadFromAnErrorResponseToo(t *testing.T) {
	r := &reports{}
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		io.WriteString(w, `{"error":{},"usage":{"input_tokens":3}}`)
	})
	s, secret, _ := meteredBehind(t, up, registry.ProfileAnthropic, false, r)
	s.send("POST", "/v1/messages", `{}`, withBearer(secret))
	if u := r.only(t); !u.Seen || u.InputTokens != 3 {
		t.Errorf("usage = %+v", u)
	}
}

func TestAGenericUpstreamIsNotRead(t *testing.T) {
	r := &reports{}
	s, secret, _ := meteredBehind(t, answering("application/json", `{"usage":{"prompt_tokens":5}}`), registry.ProfileGeneric, true, r)
	s.send("POST", "/v1/chat/completions", `{"stream":true}`, withBearer(secret))
	r.none(t)
}

// A request kman refuses reached no upstream, so it has nothing to read.
func TestARefusedRequestReportsNoUsage(t *testing.T) {
	r := &reports{}
	s, _, _ := meteredBehind(t, answering("application/json", `{}`), registry.ProfileOpenAI, true, r)
	s.send("POST", "/v1/chat/completions", `{}`, nil)
	r.none(t)
}

func TestAStreamReachesTheUpstreamAskingForItsUsage(t *testing.T) {
	rec := newRecording()
	r := &reports{}
	s, secret, _ := meteredBehind(t, rec, registry.ProfileOpenAI, true, r)
	h := withBearer(secret)
	h.Set("Accept-Encoding", "gzip")
	const sent = `{"model":"m","stream":true}`
	s.send("POST", "/v1/chat/completions?a=1", sent, h)
	got := rec.next(t)
	const want = `{"model":"m","stream":true,"stream_options":{"include_usage":true}}`
	if got.body != want || got.uri != "/v1/chat/completions?a=1" {
		t.Errorf("the upstream got %s %s", got.uri, got.body)
	}
	if n := got.header.Get("Content-Length"); n != strconv.Itoa(len(want)) {
		t.Errorf("Content-Length = %s for %d bytes", n, len(want))
	}
	if v := got.header.Get("Accept-Encoding"); v != "identity" {
		t.Errorf("Accept-Encoding = %q", v)
	}
	if v := got.header.Get("Authorization"); v != "Bearer "+credential {
		t.Errorf("Authorization = %q", v)
	}
}

// An upstream on the generic profile gets the app's request as it was sent.
func TestAGenericUpstreamGetsTheRequestUnchanged(t *testing.T) {
	rec := newRecording()
	r := &reports{}
	s, secret, _ := meteredBehind(t, rec, registry.ProfileGeneric, false, r)
	h := withBearer(secret)
	h.Set("Accept-Encoding", "gzip")
	s.send("POST", "/v1/chat/completions", `{"stream":true}`, h)
	got := rec.next(t)
	if got.body != `{"stream":true}` || got.header.Get("Accept-Encoding") != "gzip" {
		t.Errorf("the upstream got %q, Accept-Encoding %q", got.body, got.header.Get("Accept-Encoding"))
	}
}

// An upstream that compresses when told not to still reaches the app whole, with
// nothing read from it.
func TestAnEncodedResponsePassesThroughUnread(t *testing.T) {
	r := &reports{}
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		io.WriteString(w, "\x1f\x8bnot really")
	})
	s, secret, _ := meteredBehind(t, up, registry.ProfileOpenAI, false, r)
	resp, body := s.send("POST", "/v1/chat/completions", `{}`, withBearer(secret))
	if body != "\x1f\x8bnot really" || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("response = %v %q", resp.Header, body)
	}
	r.none(t)
}

// Reading usage must not hold a stream back: each event arrives before the next
// is sent, and the usage in the last is read all the same.
func TestAMeteredStreamStillArrivesAsItIsMade(t *testing.T) {
	next := make(chan struct{})
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`data: {"model":"m","choices":[{"delta":{"content":"a"}}],"usage":null}` + "\n\n",
			`data: {"model":"m","choices":[{"delta":{"content":"b"}}],"usage":null}` + "\n\n",
			`data: {"model":"m","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}` + "\n\n",
			"data: [DONE]\n\n",
		} {
			io.WriteString(w, event)
			w.(http.Flusher).Flush()
			<-next
		}
	})
	r := &reports{}
	s, secret, _ := meteredBehind(t, up, registry.ProfileOpenAI, true, r)
	req, err := http.NewRequestWithContext(t.Context(), "POST", s.front.URL+"/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = withBearer(secret)
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewReader(resp.Body)
	var sent int
	for {
		line, err := lines.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data:") {
			sent++
			next <- struct{}{}
		}
	}
	if sent != 4 {
		t.Errorf("%d events arrived", sent)
	}
	if u := r.only(t); !u.Seen || u.InputTokens != 4 || u.OutputTokens != 2 {
		t.Errorf("usage = %+v", u)
	}
}

// What was reported before a stream broke is kept, for settlement to decide on.
func TestUsageReportedBeforeAStreamBrokeIsKept(t *testing.T) {
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":9,"output_tokens":1}}}`+"\n\n")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	r := &reports{}
	s, secret, _ := meteredBehind(t, up, registry.ProfileAnthropic, false, r)
	req, err := http.NewRequestWithContext(t.Context(), "POST", s.front.URL+"/v1/messages", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = withBearer(secret)
	if resp, err := client().Do(req); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	s.front.Close() // waits for the handler, and so for settle
	if u := r.only(t); u.InputTokens != 9 || u.Model != "claude-x" {
		t.Errorf("usage = %+v", u)
	}
}

// A body kman can't read goes on as it came.
func TestABodyThatIsNotJSONIsForwardedAsItCame(t *testing.T) {
	rec := newRecording()
	r := &reports{}
	s, secret, _ := meteredBehind(t, rec, registry.ProfileOpenAI, true, r)
	s.send("POST", "/v1/chat/completions", `"stream":true`, withBearer(secret))
	if got := rec.next(t); got.body != `"stream":true` {
		t.Errorf("the upstream got %q", got.body)
	}
}

// The report of what a response used arrives before the app has the whole of it, as
// the settle does, so an app that reads the response and then looks sees it.
func TestUsageIsReportedBeforeTheResponseIsComplete(t *testing.T) {
	r := &reports{}
	up := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		io.WriteString(w, `{"error":{},"usage":{"input_tokens":3}}`)
	})
	s, secret, _ := meteredBehind(t, up, registry.ProfileAnthropic, false, r)
	w := &endWatcher{header: http.Header{}, reserved: func() int64 {
		r.mu.Lock()
		defer r.mu.Unlock()
		return int64(len(r.got))
	}}
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/messages", strings.NewReader(`{}`))
	req.Header = withBearer(secret)
	s.gw.ServeHTTP(w, req)
	if !w.wrote || w.atLast != 1 {
		t.Errorf("%d usage reports had arrived when the response's last byte went out, want 1", w.atLast)
	}
}
