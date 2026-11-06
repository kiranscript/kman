package e2e_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// transcript is a recorded conversation with an upstream: what it was asked, in
// order, and what it answered. Transcripts live in testdata/transcripts.
type transcript struct {
	Note string `json:"note"`
	// Profile is the profile to read it under. internal/profiles reads the same
	// files, and holds each exchange's Usage to what it reads from the response.
	Profile   string     `json:"profile"`
	Exchanges []exchange `json:"exchanges"`
}

type exchange struct {
	Request struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"request"`
	Response struct {
		Status  int                 `json:"status"`
		Headers map[string][]string `json:"headers"`
		Chunks  []string            `json:"chunks"` // each is written and flushed
	} `json:"response"`
	Usage json.RawMessage `json:"usage"` // for internal/profiles: not used here
}

// received is a request as the mock upstream got it.
type received struct {
	Method, URI string
	Header      http.Header
	Body        string
}

// mockUpstream answers from a transcript and remembers what it was sent. A
// request the transcript doesn't expect fails the test.
type mockUpstream struct {
	srv *httptest.Server

	host string
	port int

	flows []exchange
	mu    sync.Mutex
	next  int
	seen  []received
}

// newMock starts an upstream replaying testdata/transcripts/<name>.json. With no
// name it expects nothing at all.
func newMock(t *testing.T, name string) *mockUpstream {
	t.Helper()
	m := &mockUpstream{}
	if name != "" {
		raw, err := os.ReadFile(filepath.Join("testdata", "transcripts", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var tr transcript
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&tr); err != nil {
			t.Fatalf("transcript %s: %v", name, err)
		}
		m.flows = tr.Exchanges
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.serve(t, w, r) }))
	t.Cleanup(m.srv.Close)
	host, port, err := net.SplitHostPort(m.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	m.host = host
	if m.port, err = strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *mockUpstream) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("the mock upstream reading a body: %v", err)
	}
	m.mu.Lock()
	m.seen = append(m.seen, received{r.Method, r.RequestURI, r.Header.Clone(), string(body)})
	var ex *exchange
	if m.next < len(m.flows) {
		ex = &m.flows[m.next]
		m.next++
	}
	m.mu.Unlock()
	if ex == nil || ex.Request.Method != r.Method || ex.Request.Path != r.URL.Path {
		t.Errorf("the mock upstream did not expect %s %s", r.Method, r.URL.Path)
		http.Error(w, "not in the transcript", http.StatusTeapot)
		return
	}
	for name, values := range ex.Response.Headers {
		w.Header()[http.CanonicalHeaderKey(name)] = values
	}
	w.WriteHeader(ex.Response.Status)
	for _, chunk := range ex.Response.Chunks {
		io.WriteString(w, chunk)
		w.(http.Flusher).Flush()
	}
}

// requests is everything the upstream has been sent.
func (m *mockUpstream) requests() []received {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]received(nil), m.seen...)
}

// only is the one request the upstream must have been sent.
func (m *mockUpstream) only(t *testing.T) received {
	t.Helper()
	got := m.requests()
	if len(got) != 1 {
		t.Fatalf("the upstream got %d requests, want 1: %+v", len(got), got)
	}
	return got[0]
}

// proxied is the real binary, with a fresh data directory, in front of a mock
// upstream, with a virtual key on it.
type proxied struct {
	t      *testing.T
	h      *home
	s      *server
	mock   *mockUpstream
	secret string // the virtual key
}

// behindKman starts kman in front of a mock upstream replaying a transcript.
func behindKman(t *testing.T, name string) *proxied {
	t.Helper()
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	mock := newMock(t, name)
	h.addUpstream(t, "mock", mock.host, mock.port)
	return &proxied{t: t, h: h, s: s, mock: mock, secret: h.createKey(t, "my-app", "mock")}
}

// behindKmanAs is behindKman with an upstream on an LLM profile, which asks
// streams for their usage on the OpenAI one, as its preset does.
func behindKmanAs(t *testing.T, name, profile string) *proxied {
	t.Helper()
	h := installed(t)
	s := h.serve(t)
	h.login(t, s)
	mock := newMock(t, name)
	flags := []string{"--profile", profile}
	if profile == "openai" {
		flags = append(flags, "--inject-usage")
	}
	h.addUpstream(t, "mock", mock.host, mock.port, flags...)
	return &proxied{t: t, h: h, s: s, mock: mock, secret: h.createKey(t, "my-app", "mock")}
}

// addUpstream adds a plain-HTTP upstream with the sentinel as its credential.
func (h *home) addUpstream(t *testing.T, name, host string, port int, flags ...string) {
	t.Helper()
	args := append([]string{"upstream", "add", name, "--host", host, "--port", strconv.Itoa(port), "--no-tls", "--path", "* /**"}, flags...)
	stdout, stderr, code := h.runWithInput(t, sentinel+"\n", args...)
	if code != 0 {
		t.Fatalf("upstream add exited %d:\n%s%s", code, stdout, stderr)
	}
}

// createKey makes a virtual key and returns its secret.
func (h *home) createKey(t *testing.T, name, upstream string) string {
	t.Helper()
	out, _ := h.mustRun(t, "key", "create", name, "--upstream", upstream, "--json")
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Secret == "" {
		t.Fatalf("key create --json printed %q: %v", out, err)
	}
	return created.Secret
}

// send makes a request of the proxy listener with the key as a bearer token,
// following no redirect, and reads the whole response.
func (p *proxied) send(method, uri string, body io.Reader) (*http.Response, string) {
	p.t.Helper()
	return sendThrough(p.t, p.s, method, uri, body, http.Header{"Authorization": {"Bearer " + p.secret}})
}

func sendThrough(t *testing.T, s *server, method, uri string, body io.Reader, header http.Header) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, "http://"+s.proxy+uri, body)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range header {
		req.Header[name] = values
	}
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, uri, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the response: %v", method, uri, err)
	}
	return resp, string(b)
}

// wantError checks a response is kman's error with this code.
func wantError(t *testing.T, resp *http.Response, body string, status int, code string) {
	t.Helper()
	if resp.StatusCode != status || resp.Header.Get("X-Kman-Error") != code {
		t.Errorf("response = %d, X-Kman-Error %q, want %d %s:\n%s", resp.StatusCode, resp.Header.Get("X-Kman-Error"), status, code, body)
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error.Code != code || e.Error.Message == "" || e.Error.Type == "" {
		t.Errorf("the body is not an OpenAI-shaped error with code %s: %v\n%s", code, err, body)
	}
}
