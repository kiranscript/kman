package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type canaryShown struct {
	ID      string
	State   string
	Fires   int64
	Planted string
}

func (h *home) showCanary(t *testing.T, ref string) canaryShown {
	t.Helper()
	out, _ := h.mustRun(t, "canary", "show", ref, "--json")
	var c canaryShown
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("canary show --json printed %q: %v", out, err)
	}
	return c
}

// count is how many of what the sink was sent hold every part.
func (k *sink) count(parts ...string) int {
	n := 0
	for _, r := range k.received() {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(r.body, p)
		}
		if ok {
			n++
		}
	}
	return n
}

// #87 end to end, by CLI alone: a canary is made, it answers like any bad key,
// its use alerts with where it was planted and where it came from (once, then a
// count), an ignored address does not fire it, and a reset arms it again.
func TestACanaryFiresAnAlertThatNamesItAndWhereItWasPlanted(t *testing.T) {
	p := behindKman(t, "")
	hung, ok := newSink(t, 0), newSink(t, 200)
	p.h.addWebhook(t, "hung", hung)
	p.h.addWebhook(t, "pager", ok)

	out, _ := p.h.mustRun(t, "canary", "create", "decoy", "--planted", "the deploy repo's .env", "--format", "shell",
		"--ignore", "127.0.0.1", "--json")
	var created struct {
		Canary  struct{ ID string }
		Secret  string
		Snippet string
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Secret == "" || created.Snippet != "export OPENAI_API_KEY="+created.Secret+"\n" {
		t.Fatalf("canary create --json printed %q: %v", out, err)
	}
	id := created.Canary.ID
	use := func() (*http.Response, string) {
		return sendThrough(t, p.s, "POST", "/v1/chat/completions", strings.NewReader("{}"), http.Header{"Authorization": {"Bearer " + created.Secret}})
	}

	// The test's own address is on the ignore list: the canary answers like any
	// bad key and does not fire, but the use is still an event.
	resp, body := use()
	wantError(t, resp, body, 401, "invalid_key")
	if c := p.h.showCanary(t, "decoy"); c.State != "armed" || c.Fires != 0 {
		t.Errorf("after an ignored use: %+v, want armed", c)
	}
	// Events are written in batches, every 5 s at most.
	deadline := time.Now().Add(15 * time.Second)
	for {
		events, _ := p.h.mustRun(t, "events", "--key", id, "--refused", "--json")
		if strings.Contains(events, "invalid_key") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ignored use left no event:\n%s", events)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := ok.count(`"kind":"canary"`); n != 0 {
		t.Fatalf("an ignored address raised %d canary alerts", n)
	}

	// Without the ignore list the same address fires it, with another channel hung.
	p.h.mustRun(t, "canary", "update", "decoy", "--no-ignore")
	start := time.Now()
	resp, body = use()
	wantError(t, resp, body, 401, "invalid_key")
	got := ok.waitFor(t, `"kind":"canary"`, id, "decoy", "the deploy repo's .env", "127.0.0.1", "kman canary reset "+id)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the canary alert took %v with another channel hung, want under 5s", d)
	}
	var alert struct {
		Severity string
		Evidence []struct{ Label, Value string }
	}
	if err := json.Unmarshal([]byte(got.body), &alert); err != nil || alert.Severity != "critical" {
		t.Errorf("alert = %+v (%v):\n%s", alert, err, got.body)
	}
	labels := map[string]string{}
	for _, e := range alert.Evidence {
		labels[e.Label] = e.Value
	}
	for _, label := range []string{"Planted", "From", "Client", "Method"} {
		if labels[label] == "" {
			t.Errorf("the alert has no %s: %v", label, labels)
		}
	}
	if c := p.h.showCanary(t, "decoy"); c.State != "fired" || c.Fires != 1 {
		t.Errorf("after its use: %+v, want fired once", c)
	}
	list, _ := p.h.mustRun(t, "canary", "list")
	if !strings.Contains(list, "fired") || !strings.Contains(list, "the deploy repo's .env") {
		t.Errorf("canary list:\n%s", list)
	}
	// Neither the alert nor the lists give the secret away.
	for _, text := range []string{got.body, list} {
		if strings.Contains(text, created.Secret) || strings.Contains(text, created.Secret[len(created.Secret)-12:]) {
			t.Errorf("the secret shows again:\n%s", text)
		}
	}

	// While it is fired, more uses are counted and do not alert again at once.
	use()
	use()
	deadline = time.Now().Add(10 * time.Second)
	for p.h.showCanary(t, "decoy").Fires < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("uses of a fired canary were not counted: %+v", p.h.showCanary(t, "decoy"))
		}
		time.Sleep(25 * time.Millisecond)
	}
	if n := ok.count(`"kind":"canary"`); n != 1 {
		t.Errorf("%d canary alerts for three uses, want 1", n)
	}

	// A reset arms it again, and its next use alerts at once.
	p.h.mustRun(t, "canary", "reset", "decoy")
	if c := p.h.showCanary(t, "decoy"); c.State != "armed" {
		t.Errorf("after a reset: %+v", c)
	}
	use()
	deadline = time.Now().Add(10 * time.Second)
	for ok.count(`"kind":"canary"`) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the use after a reset raised no alert; %d canary alerts", ok.count(`"kind":"canary"`))
		}
		time.Sleep(25 * time.Millisecond)
	}

	// A canary is not a key at risk, and not in the list of keys.
	if keys, _ := p.h.mustRun(t, "key", "list"); strings.Contains(keys, "decoy") {
		t.Errorf("key list shows the canary:\n%s", keys)
	}
	if exposure, _ := p.h.mustRun(t, "exposure"); strings.Contains(exposure, "decoy") || strings.Contains(exposure, id) {
		t.Errorf("exposure counts the canary:\n%s", exposure)
	}
}
