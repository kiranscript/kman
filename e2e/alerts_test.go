package e2e_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sink is a local alert receiver: a channel's address in these tests. It
// answers with status, and keeps what it was sent.
type sink struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []sunk
	// status is what it answers; 0 makes it hang until the test ends.
	status  int
	release chan struct{}
}

type sunk struct {
	header http.Header
	body   string
}

func newSink(t *testing.T, status int) *sink {
	t.Helper()
	k := &sink{status: status, release: make(chan struct{})}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		k.mu.Lock()
		k.got = append(k.got, sunk{r.Header.Clone(), string(b)})
		st := k.status
		k.mu.Unlock()
		if st == 0 {
			select {
			case <-k.release:
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(st)
	}))
	t.Cleanup(func() { close(k.release); k.srv.Close() })
	return k
}

func (k *sink) setStatus(st int) { k.mu.Lock(); k.status = st; k.mu.Unlock() }

func (k *sink) received() []sunk {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]sunk(nil), k.got...)
}

// waitFor waits for a request whose body holds every part, and returns it.
func (k *sink) waitFor(t *testing.T, parts ...string) sunk {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		for _, r := range k.received() {
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(r.body, p)
			}
			if ok {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no alert with %q arrived; got %d:\n%v", parts, len(k.received()), k.received())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// addWebhook adds a webhook channel at the sink and returns its signing secret.
func (h *home) addWebhook(t *testing.T, name string, k *sink) string {
	t.Helper()
	out, stderr, code := h.runWithInput(t, k.srv.URL+"/in/TOKEN-"+name+"\n", "channel", "add", name, "--kind", "webhook", "--min-severity", "low", "--json")
	if code != 0 {
		t.Fatalf("channel add exited %d:\n%s%s", code, out, stderr)
	}
	var created struct {
		SigningSecret string `json:"signing_secret"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.SigningSecret == "" {
		t.Fatalf("channel add --json printed %q: %v", out, err)
	}
	return created.SigningSecret
}

func TestAlertsReachAWebhookSignedAndAHungChannelHoldsUpNoOther(t *testing.T) {
	p := behindKman(t, "")
	hung, ok := newSink(t, 0), newSink(t, 200)
	p.h.addWebhook(t, "hung", hung)
	secret := p.h.addWebhook(t, "pager", ok)

	start := time.Now()
	id := p.h.showKey(t, "my-app").ID
	p.h.mustRun(t, "key", "disable", "my-app")
	r := ok.waitFor(t, "key.disable", id)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the alert took %v with another channel hung, want under 5s", d)
	}

	// The receiver verifies it as documented in the operator guide.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(r.header.Get("X-Kman-Timestamp") + "." + r.body))
	if want := "v1=" + hex.EncodeToString(mac.Sum(nil)); r.header.Get("X-Kman-Signature") != want {
		t.Errorf("X-Kman-Signature = %q, want %q", r.header.Get("X-Kman-Signature"), want)
	}
	var body struct {
		Severity string
		Key      struct{ ID, Name string }
		Commands []string
	}
	if err := json.Unmarshal([]byte(r.body), &body); err != nil || body.Key.Name != "my-app" || body.Key.ID != id || body.Severity != "medium" ||
		len(body.Commands) == 0 {
		t.Errorf("alert = %+v (%v):\n%s", body, err, r.body)
	}

	// The key is disabled, so its next use is a priority alert, through the lane
	// that does not wait on the event store.
	resp, rb := p.send("POST", "/v1/things", strings.NewReader("{}"))
	wantError(t, resp, rb, 403, "key_disabled")
	used := ok.waitFor(t, "disabled_key", id)
	if !strings.Contains(used.body, `"POST"`) || strings.Contains(used.body, "/v1/things") {
		t.Errorf("the alert should name the method and never the caller's path:\n%s", used.body)
	}

	// No alert holds a secret: not the key's, not the upstream credential, not
	// the signing secret, not a channel's address.
	for _, got := range ok.received() {
		for _, leak := range []string{p.secret, sentinel, secret, "TOKEN-pager", "TOKEN-hung"} {
			if strings.Contains(got.body, leak) {
				t.Errorf("an alert holds %q:\n%s", leak, got.body)
			}
		}
	}

	// The hung channel's alerts are held, not dropped, and kman says so.
	out, _ := p.h.mustRun(t, "channel", "list")
	if !strings.Contains(out, "hung") || !strings.Contains(out, "pager") {
		t.Errorf("channel list:\n%s", out)
	}
}

func TestAnAlertThatCouldNotBeDeliveredIsSentAfterARestart(t *testing.T) {
	p := behindKman(t, "")
	down := newSink(t, 503)
	p.h.addWebhook(t, "pager", down)
	id := p.h.showKey(t, "my-app").ID
	p.h.mustRun(t, "key", "revoke", "my-app")
	down.waitFor(t, "key.revoke", id) // tried once, refused, so it waits in the outbox

	stopServer(t, p.s)
	down.setStatus(200)
	restarted := p.h.serve(t)
	p.h.login(t, restarted)
	delivered := func() bool {
		n := 0
		for _, r := range down.received() {
			if strings.Contains(r.body, "key.revoke") {
				n++
			}
		}
		return n >= 2
	}
	deadline := time.Now().Add(30 * time.Second)
	for !delivered() {
		if time.Now().After(deadline) {
			t.Fatalf("the alert was not sent again after the restart:\n%s", restarted.logs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAChannelTestSendsAlertAndSaysWhenItFails(t *testing.T) {
	p := behindKman(t, "")
	ok := newSink(t, 200)
	p.h.addWebhook(t, "pager", ok)
	out, _ := p.h.mustRun(t, "channel", "test", "pager")
	if !strings.Contains(out, "delivered to pager") {
		t.Errorf("channel test said:\n%s", out)
	}
	ok.waitFor(t, `"kind":"test"`)
	ok.setStatus(404)
	_, stderr, code := p.h.run(t, "channel", "test", "pager")
	if code != 1 || !strings.Contains(stderr, "404") || strings.Contains(stderr, "TOKEN-pager") {
		t.Errorf("a refused test: exit %d\n%s", code, stderr)
	}
}

// A provider that answers 401 to the credential kman presents is a credential
// warning: every key on that upstream is failing. The alert names the upstream
// and the credential, and holds no secret.
func TestAProviderRefusingKmansCredentialIsAWarningThatHoldsNoSecret(t *testing.T) {
	p := behindKmanAs(t, "openai-401", "openai")
	k := newSink(t, 200)
	secret := p.h.addWebhook(t, "pager", k)

	resp, body := p.chat(chatRequest)
	if resp.StatusCode != 401 {
		t.Fatalf("the app got %d %s, want the upstream's 401", resp.StatusCode, body)
	}
	got := k.waitFor(t, "refused kman's credential", `"kind":"credential_refused"`, "mock")
	var alert struct {
		Severity string
		Evidence []struct{ Label, Value string }
		Commands []string
	}
	if err := json.Unmarshal([]byte(got.body), &alert); err != nil || alert.Severity != "high" || len(alert.Commands) == 0 {
		t.Errorf("alert = %+v (%v):\n%s", alert, err, got.body)
	}
	for _, leak := range []string{p.secret, sentinel, secret, "TOKEN-pager"} {
		if strings.Contains(got.body, leak) {
			t.Errorf("the alert holds %q:\n%s", leak, got.body)
		}
	}
	// A second refusal inside the window is held back, not a second alert.
	p.chat(chatRequest)
	time.Sleep(300 * time.Millisecond)
	n := 0
	for _, r := range k.received() {
		if strings.Contains(r.body, "refused kman's credential") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d credential warnings for two refusals, want 1", n)
	}
}
