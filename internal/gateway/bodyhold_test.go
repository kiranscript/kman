package gateway_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/gateway"
)

// The security review (docs/security-review-v0.1.0.md, finding S-1): a virtual
// key, such as a leaked one, must not be able to hold kman's connections and
// memory by sending a body slowly, or not finishing it. Each request is held to
// the same bounds whatever the key's own limits are.

// dial opens a raw connection to the gateway. It is closed with the test.
func dial(t *testing.T, s *stack) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", s.front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// startBody sends a request's head, stating a body of n bytes, and the first
// sent of them.
func startBody(t *testing.T, c net.Conn, secret string, n, sent int) {
	t.Helper()
	if _, err := fmt.Fprintf(c, "POST /v1/things HTTP/1.1\r\nHost: kman\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\n\r\n%s",
		secret, n, strings.Repeat("a", sent)); err != nil {
		t.Fatal(err)
	}
}

// wantClosed checks that the server hangs up on c without waiting for the rest
// of what it was promised.
func wantClosed(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(io.Discard, c); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal("the server is still holding a connection whose body stopped arriving")
		}
	}
}

func TestABodyThatStopsArrivingIsCutOff(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up, func(o *gateway.Options) { o.BodyStall = 100 * time.Millisecond })
	c := dial(t, s)
	startBody(t, c, secret, 1000, 3)
	wantClosed(t, c)
	up.none(t)
}

func TestABodyThatTakesTooLongIsCutOffWhateverItsPace(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up, func(o *gateway.Options) { o.BodyStall = time.Hour; o.BodyTimeout = 100 * time.Millisecond })
	c := dial(t, s)
	startBody(t, c, secret, 1000, 3)
	wantClosed(t, c)
	up.none(t)
}

// Go's server reads what is left of a request's body before it sends a
// response that came early, so a refusal would otherwise wait on the sender. A
// client with no key at all could hold connections that way.
func TestARefusalDoesNotWaitForTheBodyItWasPromised(t *testing.T) {
	s, _ := behind(t, newRecording())
	for name, key := range map[string]string{
		"a key that is not one":        "nonsense",
		"a well-formed key kman lacks": "kmn_AAAAAAAA_" + strings.Repeat("B", 49),
	} {
		t.Run(name, func(t *testing.T) {
			c := dial(t, s)
			startBody(t, c, key, 1000, 3)
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			resp, err := http.ReadResponse(bufio.NewReader(c), nil)
			if err != nil {
				t.Fatalf("no answer while the body was unfinished: %v", err)
			}
			if resp.StatusCode != 401 || !resp.Close {
				t.Errorf("status %d, closing %v: want 401 and a connection closed after it", resp.StatusCode, resp.Close)
			}
		})
	}
}

func TestABodyThatKeepsArrivingIsNotCutOff(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up) // the defaults
	c := dial(t, s)
	startBody(t, c, secret, 10, 4)
	if _, err := io.WriteString(c, "bbbbbb"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("response = %v, %v", resp, err)
	}
	if got := up.next(t).body; got != "aaaabbbbbb" {
		t.Errorf("the upstream got %q", got)
	}
}

func TestRequestBodiesInMemoryAreBoundedAcrossRequests(t *testing.T) {
	up := newRecording()
	s, secret := behind(t, up, func(o *gateway.Options) { o.MaxBuffered = 1000 })

	first, second := dial(t, s), dial(t, s)
	startBody(t, first, secret, 800, 600) // 600 bytes held, 200 to come
	waitHeld(t, s, first)
	startBody(t, second, secret, 800, 600) // would hold 1200: over the budget
	resp, err := http.ReadResponse(bufio.NewReader(second), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	wantRefusal(t, resp, string(body), 429, "rate_limited")
	if resp.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}

	// The first request is untouched, and when it ends what it held is free again.
	io.WriteString(first, strings.Repeat("a", 200))
	r1, err := http.ReadResponse(bufio.NewReader(first), nil)
	if err != nil || r1.StatusCode != 200 {
		t.Fatalf("the first request: %v, %v", r1, err)
	}
	io.Copy(io.Discard, r1.Body)
	third := dial(t, s)
	startBody(t, third, secret, 800, 800)
	r3, err := http.ReadResponse(bufio.NewReader(third), nil)
	if err != nil || r3.StatusCode != 200 {
		t.Fatalf("a request after the budget was freed: %v, %v", r3, err)
	}
}

// waitHeld returns once the server has read what c sent, which a request on
// another connection can't know: nothing is answered while a body is awaited.
func waitHeld(t *testing.T, s *stack, c net.Conn) {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); runtime.Gosched() {
		if s.gw.Buffered() >= 600 {
			return
		}
	}
	t.Fatal("the server never held the first body")
}
