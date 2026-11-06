package gateway_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/gateway"
)

// redirecting is an upstream that answers every request with a redirect, which
// counts what it gets.
func redirecting(status int, location func(self string) string) (http.Handler, *int) {
	hits := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Location", location(r.Host))
		w.WriteHeader(status)
	}), &hits
}

func TestARedirectToAnotherPlaceIsBlocked(t *testing.T) {
	for name, location := range map[string]func(string) string{
		"another host":             func(string) string { return "https://elsewhere.example/x" },
		"another host, no scheme":  func(string) string { return "//elsewhere.example/x" },
		"a host after a backslash": func(string) string { return `/\elsewhere.example/x` },
		"backslashes for slashes":  func(string) string { return `http:\\elsewhere.example\x` },
		"a user that looks right":  func(self string) string { return "http://" + self + "@elsewhere.example/x" },
		"another port":             func(self string) string { h, _, _ := net.SplitHostPort(self); return "http://" + h + ":1/x" },
		"another scheme":           func(self string) string { return "https://" + self + "/x" },
		"a host in the path":       func(self string) string { return "http://" + self + "//elsewhere.example/x" },
		"three slashes":            func(string) string { return "///elsewhere.example/x" },
		"not http":                 func(string) string { return "file:///etc/passwd" },
		"not a URL":                func(string) string { return "http://[bad" },
	} {
		t.Run(name, func(t *testing.T) {
			handler, hits := redirecting(302, location)
			s, secret := behind(t, handler)
			resp, body := s.send("GET", "/v1/things", "", withBearer(secret))
			wantRefusal(t, resp, body, 502, "upstream_unavailable")
			if v := resp.Header.Get("Location"); v != "" {
				t.Errorf("the app was told to go to %q", v)
			}
			if *hits != 1 {
				t.Errorf("the upstream got %d requests", *hits)
			}
		})
	}
}

// Every redirect status gets the same treatment.
func TestEveryRedirectStatusIsBlocked(t *testing.T) {
	for _, status := range []int{300, 301, 302, 303, 307, 308} {
		handler, _ := redirecting(status, func(string) string { return "https://elsewhere.example/x" })
		s, secret := behind(t, handler)
		resp, body := s.send("GET", "/", "", withBearer(secret))
		wantRefusal(t, resp, body, 502, "upstream_unavailable")
	}
}

// An app follows a redirect itself, so it must stay on kman: an absolute URL
// would send it, and its virtual key, straight to the upstream.
func TestARedirectWithinTheUpstreamKeepsTheAppOnKman(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location func(self string) string
		want     string
	}{
		{"a relative path", func(string) string { return "/v1/other?a=1" }, "/v1/other?a=1"},
		{"a path relative to this one", func(string) string { return "other" }, "other"},
		{"an absolute URL on the same host", func(self string) string { return "http://" + self + "/v1/other?a=1" }, "/v1/other?a=1"},
		{"an absolute URL in another case", func(self string) string { return "HTTP://" + strings.ToUpper(self) + "/v1/other" }, "/v1/other"},
		{"an absolute URL with no path", func(self string) string { return "http://" + self }, "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, hits := redirecting(307, tc.location)
			s, secret := behind(t, handler)
			resp, _ := s.send("GET", "/v1/things", "", withBearer(secret))
			if resp.StatusCode != 307 {
				t.Errorf("status = %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != tc.want {
				t.Errorf("Location = %q, want %q", got, tc.want)
			}
			if *hits != 1 {
				t.Errorf("the upstream got %d requests: kman must not follow it", *hits)
			}
		})
	}
}

// A redirect may name the TLS server name rather than the address dialled.
func TestARedirectMayNameTheServerName(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://"+r.Host+"/v1/other") // r.Host names the server name
		w.WriteHeader(302)
	}))
	t.Cleanup(up.Close)
	s := newStack(t, func(o *gateway.Options) { o.RootCAs = poolOf(up) })
	k, secret := newKey(t)
	spec := specOf(t, up.URL)
	spec.ServerName = "example.com"
	s.publish(spec, k)
	resp, body := s.send("GET", "/", "", withBearer(secret))
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/v1/other" {
		t.Errorf("response = %d %q: %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
}

// A 3xx with no Location is not a redirect.
func TestANotModifiedPassesThrough(t *testing.T) {
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(304)
	}))
	if resp, _ := s.send("GET", "/", "", withBearer(secret)); resp.StatusCode != 304 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestAnErrorFromTheUpstreamPassesThrough(t *testing.T) {
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"code":"from_the_upstream"}}`)
	}))
	resp, body := s.send("POST", "/", "{}", withBearer(secret))
	if resp.StatusCode != 429 || body != `{"error":{"code":"from_the_upstream"}}` {
		t.Errorf("response = %d %s", resp.StatusCode, body)
	}
	if v := resp.Header.Get("X-Kman-Error"); v != "" {
		t.Errorf("X-Kman-Error = %q: kman refused nothing", v)
	}
}

// closedAddr is a URL where nothing listens.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return "http://" + l.Addr().String()
}

// Nothing secret reaches a log or the app when an upstream is down, including the
// query string, which net/http's own error would quote.
func TestAnUpstreamThatIsDownIsAnErrorThatGivesNothingAway(t *testing.T) {
	s := newStack(t)
	k, secret := newKey(t)
	s.publish(specOf(t, closedAddr(t)), k)

	const query = "sekret=in-the-query"
	resp, body := s.send("POST", "/v1/things?"+query, "the-request-body", withBearer(secret))

	wantRefusal(t, resp, body, 502, "upstream_unavailable")
	logs := s.logs.String()
	if !strings.Contains(logs, k.ID) {
		t.Errorf("the log does not name the key:\n%s", logs)
	}
	for _, leak := range []string{query, secret, credential, "the-request-body"} {
		if strings.Contains(logs, leak) || strings.Contains(body, leak) {
			t.Errorf("%q leaked:\nlog: %s\nbody: %s", leak, logs, body)
		}
	}
}

// The app gets a 502 for a certificate it can't check, and no credential leaves.
func TestAnUpstreamWithAnUntrustedCertificateIsUnavailable(t *testing.T) {
	up := newRecording()
	srv := httptest.NewTLSServer(up)
	t.Cleanup(srv.Close)
	s := newStack(t) // trusts only the system's roots
	k, secret := newKey(t)
	s.publish(specOf(t, srv.URL), k)
	resp, body := s.send("GET", "/", "", withBearer(secret))
	wantRefusal(t, resp, body, 502, "upstream_unavailable")
	up.none(t)
}

func TestTLSChecksTheServerNameWhenOneIsGiven(t *testing.T) {
	up := newRecording()
	srv := httptest.NewTLSServer(up) // its certificate names example.com
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name, serverName string
		status           int
	}{
		{"the name on the certificate", "example.com", 200},
		{"another name", "wrong.example", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, func(o *gateway.Options) { o.RootCAs = poolOf(srv) })
			k, secret := newKey(t)
			spec := specOf(t, srv.URL)
			spec.ServerName = tc.serverName
			s.publish(spec, k)
			resp, body := s.send("GET", "/", "", withBearer(secret))
			if resp.StatusCode != tc.status {
				t.Fatalf("response = %d %s", resp.StatusCode, body)
			}
			if tc.status == 200 {
				if got := up.next(t); got.host != net.JoinHostPort(tc.serverName, strconv.Itoa(spec.Port)) {
					t.Errorf("Host = %q, want %s on port %d", got.host, tc.serverName, spec.Port)
				}
			} else {
				up.none(t)
			}
		})
	}
}

func TestAnUpstreamOverTLSIsReached(t *testing.T) {
	up := newRecording()
	srv := httptest.NewTLSServer(up)
	t.Cleanup(srv.Close)
	s := newStack(t, func(o *gateway.Options) { o.RootCAs = poolOf(srv) })
	k, secret := newKey(t)
	s.publish(specOf(t, srv.URL), k)
	if resp, body := s.send("POST", "/v1/x", "{}", withBearer(secret)); resp.StatusCode != 200 {
		t.Fatalf("response = %d %s", resp.StatusCode, body)
	}
	if got := up.next(t); got.header.Get("Authorization") != "Bearer "+credential {
		t.Errorf("Authorization = %q", got.header.Get("Authorization"))
	}
}

// A stream reaches the app as it's made: each event arrives before the next is
// sent. If kman held any back this would stop at the context's deadline.
func TestAStreamPassesThroughAsItArrives(t *testing.T) {
	release := make(chan struct{})
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "data: second\n\n")
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.front.URL+"/v1/stream", strings.NewReader("{}"))
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
	first, err := lines.ReadString('\n')
	if err != nil || first != "data: first\n" {
		t.Fatalf("first = %q, %v", first, err)
	}
	close(release)
	rest, err := io.ReadAll(lines)
	if err != nil || string(rest) != "\ndata: second\n\n" {
		t.Errorf("rest = %q, %v", rest, err)
	}
}

// A headers-only start of a stream reaches the app straight away too.
func TestTheStartOfAResponseIsNotHeldBack(t *testing.T) {
	release := make(chan struct{})
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-release
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	defer close(release)
	req, err := http.NewRequestWithContext(ctx, "GET", s.front.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = withBearer(secret)
	resp, err := client().Do(req)
	if err != nil {
		t.Fatalf("the headers did not arrive: %v", err)
	}
	resp.Body.Close()
}

// An upstream that breaks off must not look like a response that ended: an app
// would take half an answer for the whole.
func TestAnUpstreamBreakingOffCutsTheAppsConnection(t *testing.T) {
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	req, err := http.NewRequestWithContext(t.Context(), "GET", s.front.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = withBearer(secret)
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("the response read as complete")
	}
}

func TestTrailersPassThrough(t *testing.T) {
	s, secret := behind(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Checksum")
		io.WriteString(w, "body")
		w.Header().Set("X-Checksum", "abc")
	}))
	req, err := http.NewRequestWithContext(t.Context(), "GET", s.front.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = withBearer(secret)
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if got := resp.Trailer.Get("X-Checksum"); got != "abc" {
		t.Errorf("trailer = %q, headers %v", got, resp.Header)
	}
}

// An app that gives up stops the upstream's work too, and kman logs no failure
// for it: nothing failed.
func TestAnAppGoingAwayCancelsTheUpstreamRequest(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	s, secret := behind(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, "GET", s.front.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = withBearer(secret)
	done := make(chan error, 1)
	go func() {
		resp, err := client().Do(req)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	<-done
	<-cancelled
}
