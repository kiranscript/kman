package e2e_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// feedEvent is the fields of an event the tests read.
type feedEvent struct {
	Kind            string `json:"kind"`
	KeyID           string `json:"key_id"`
	KeyName         string `json:"key_name"`
	UnidentifiedKey bool   `json:"unidentified_key"`
	Client          string `json:"client"`
	ClientAddress   string `json:"client_address"`
	Withheld        bool   `json:"client_address_withheld"`
	Country         string `json:"country"`
	ASN             int64  `json:"asn"`
	ASNLabel        string `json:"asn_label"`
	Operation       string `json:"operation"`
	Status          int    `json:"status"`
	RefusalCode     string `json:"refusal_code"`
}

// followEvents starts kman events --follow --json and returns what it prints, a
// line at a time, and the command.
func followEvents(t *testing.T, h *home, args ...string) (<-chan feedEvent, *exec.Cmd) {
	t.Helper()
	cmd := h.command(append([]string{"events", "--follow", "--json", "--limit", "1"}, args...)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	t.Cleanup(func() { cmd.Process.Kill(); <-exited })
	lines := make(chan feedEvent, 100)
	go func() {
		defer close(exited)
		defer close(lines)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			var e feedEvent
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				t.Errorf("kman events --follow --json printed %q: %v", sc.Text(), err)
				continue
			}
			lines <- e
		}
		cmd.Wait()
	}()
	return lines, cmd
}

// next waits for the next event that is, or until it has been tried too long;
// try is called each half second to make one, since an event from before the
// follower subscribed is not in the stream.
func next(t *testing.T, lines <-chan feedEvent, try func(), ok func(feedEvent) bool) feedEvent {
	t.Helper()
	deadline := time.After(20 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case e, open := <-lines:
			if !open {
				t.Fatal("kman events --follow ended")
			}
			if ok(e) {
				return e
			}
		case <-tick.C:
			try()
		case <-deadline:
			t.Fatal("the event never showed in kman events --follow")
		}
	}
}

// kman events --follow shows requests through the real binary as they happen:
// a forwarded request of a named key, with where it came from, and a request
// with a key that does not exist, marked as such. It never shows an address
// until the install turns client addresses on.
func TestEventsFollowShowsRequestsAsTheyHappenThroughTheRealBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot send SIGINT to another process")
	}
	p, h := geoipProxied(t)
	lines, cmd := followEvents(t, h)

	e := next(t, lines, func() { p.send("POST", "/v1/things?api_key=sk-leaked", strings.NewReader(`{"name":"x"}`)) },
		func(e feedEvent) bool { return e.Operation == "POST /v1/things" })
	if e.Kind != "forwarded" || e.KeyName != "my-app" || e.KeyID == "" || e.Status != 201 || e.Country != "US" || e.ASN != 64500 || e.ASNLabel != "Test Network" {
		t.Errorf("a forwarded request = %+v", e)
	}
	// What called, which the Go client of the test says it is: shown with client
	// addresses off, for it is a program and not a person.
	if e.Client != "go-http-client/1" {
		t.Errorf("the client of a forwarded request = %q, want go-http-client/1", e.Client)
	}
	if e.ClientAddress != "" || !e.Withheld {
		t.Errorf("the address of a forwarded request is out with client addresses off: %+v", e)
	}

	// A key that does not exist: probing, which shows with no key. The gateway does
	// not place a request that proved no key, so it has no country or AS.
	bad := "kmn_zzzzzzzz_" + strings.Repeat("z", 49)
	const probingUA = "python-requests/2.31.0 (scanner; probe@example.com)"
	probe := next(t, lines, func() {
		sendThrough(t, p.s, "POST", "/v1/things", strings.NewReader("{}"), http.Header{"Authorization": {"Bearer " + bad}, "User-Agent": {probingUA}})
	}, func(e feedEvent) bool { return e.RefusalCode == "invalid_key" })
	if probe.Kind != "refused" || !probe.UnidentifiedKey || probe.KeyID != "" || probe.Status != http.StatusUnauthorized {
		t.Errorf("a request with an invalid key = %+v, want it refused and marked", probe)
	}
	// Its client is recorded too, reduced to a product and a major version: probing
	// is told apart by what it comes from.
	if probe.Client != "python-requests/2" {
		t.Errorf("the client of a request with an invalid key = %q, want python-requests/2", probe.Client)
	}

	// The list reads the store, which the writer fills every 5 seconds.
	var listed struct {
		Events []feedEvent
		More   bool
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, _ := h.mustRun(t, "events", "--refused", "--json")
		if err := json.Unmarshal([]byte(out), &listed); err != nil {
			t.Fatalf("kman events --json printed %q: %v", out, err)
		}
		if len(listed.Events) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("kman events never listed the refused request")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if r := listed.Events[0]; r.RefusalCode != "invalid_key" || !r.UnidentifiedKey || r.ClientAddress != "" || r.Client != "python-requests/2" {
		t.Errorf("kman events --refused = %+v", r)
	}
	out, _ := h.mustRun(t, "events")
	for _, want := range []string{"my-app", "(no key)", "python-requests/2", "go-http-client/1", "AS64500 Test Network", "invalid_key", "POST /v1/things", "expose_client_addresses"} {
		if !strings.Contains(out, want) {
			t.Errorf("kman events lacks %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"api_key", "sk-leaked", "127.0.0.1", "::1", p.secret, bad, "scanner", "probe@example.com", "2.31"} {
		if strings.Contains(out, leak) {
			t.Errorf("kman events printed %q:\n%s", leak, out)
		}
	}

	// With client addresses on, a new follower shows where the request came from.
	h.mustRun(t, "settings", "set", "expose_client_addresses", "on")
	lines2, _ := followEvents(t, h, "--key", "my-app")
	e = next(t, lines2, func() { p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`)) },
		func(e feedEvent) bool { return e.ClientAddress != "" })
	if e.ClientAddress != "127.0.0.1" && e.ClientAddress != "::1" || e.Withheld {
		t.Errorf("with client addresses on: %+v", e)
	}

	// Interrupting the follower ends it cleanly.
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	for range lines {
	}
}

// A kman that is told to stop does not wait for a client that is following.
func TestKmanStopsWhileAClientFollowsTheFeed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot send SIGTERM to another process")
	}
	p := behindKman(t, "generic-json")
	lines, cmd := followEvents(t, p.h)
	_ = cmd
	next(t, lines, func() { p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`)) }, func(feedEvent) bool { return true })
	start := time.Now()
	if err := p.s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := p.s.wait(t); code != 0 {
		t.Fatalf("kman serve exited %d:\n%s", code, p.s.logs)
	}
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("kman took %v to stop with a follower, which it should not wait for", took)
	}
	for range lines { // the follower ends with kman, and says so
	}
	_ = cmd
}
