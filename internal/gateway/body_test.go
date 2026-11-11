package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/gateway"
)

func limitedTo(n int64) func(*gateway.Options) {
	return func(o *gateway.Options) { o.MaxBody = n }
}

// sendBody is send with a body whose length the client states or doesn't.
func (s *stack) sendBody(body string, stated bool, secret string) (*http.Response, string) {
	s.t.Helper()
	var rd io.Reader = strings.NewReader(body)
	if !stated {
		rd = io.MultiReader(rd) // no length, so it goes chunked
	}
	req, err := http.NewRequestWithContext(s.t.Context(), "POST", s.front.URL+"/v1/things", rd)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header = withBearer(secret)
	resp, err := client().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp, string(b)
}

func TestABodyOverTheLimitIsRefusedAndNeverForwarded(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stated bool
	}{{"with its length stated", true}, {"chunked", false}} {
		t.Run(tc.name, func(t *testing.T) {
			up := newRecording()
			s, secret := behind(t, up, limitedTo(10))
			resp, body := s.sendBody(strings.Repeat("x", 11), tc.stated, secret)
			wantRefusal(t, resp, body, 413, "request_too_large")
			up.none(t)
		})
	}
}

func TestABodyAtTheLimitIsForwardedWhole(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stated bool
	}{{"with its length stated", true}, {"chunked", false}} {
		t.Run(tc.name, func(t *testing.T) {
			up := newRecording()
			s, secret := behind(t, up, limitedTo(10))
			want := strings.Repeat("x", 10)
			resp, body := s.sendBody(want, tc.stated, secret)
			if resp.StatusCode != 200 {
				t.Fatalf("response = %d %s", resp.StatusCode, body)
			}
			got := up.next(t)
			if got.body != want {
				t.Errorf("the upstream got %q", got.body)
			}
			// Buffered, so the length is known and nothing goes chunked.
			if v := got.header.Get("Content-Length"); v != "10" {
				t.Errorf("Content-Length = %q", v)
			}
		})
	}
}

// Whoever has no key can't make kman hold a body.
func TestAnInvalidKeyIsRefusedBeforeTheBodyIsRead(t *testing.T) {
	s, _ := behind(t, newRecording(), limitedTo(10))
	read := false
	req := httptest.NewRequest("POST", "/", readerFunc(func([]byte) (int, error) {
		read = true
		return 0, io.EOF
	}))
	req.Header = withBearer("kmn_nonsense")
	resp := s.direct(req)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if read {
		t.Error("the body was read")
	}
}

// The limit has a default: nothing makes an operator pick one to stay bounded.
func TestTheDefaultLimitIsThirtyTwoMebibytes(t *testing.T) {
	if gateway.DefaultMaxBody != 32<<20 {
		t.Errorf("DefaultMaxBody = %d", gateway.DefaultMaxBody)
	}
	up := newRecording()
	s, secret := behind(t, up)
	req := httptest.NewRequest("POST", "/", strings.NewReader("x"))
	req.ContentLength = gateway.DefaultMaxBody + 1
	req.Header = withBearer(secret)
	if resp := s.direct(req); resp.StatusCode != 413 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	up.none(t)
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
