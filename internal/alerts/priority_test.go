package alerts_test

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

// priorityLane runs a bus whose priority lane the service listens to.
func (in *install) priorityLane() (bus *events.Bus, stop func()) {
	bus = events.New(events.Options{})
	sub := bus.Subscribe("alerts", events.SubOptions{PriorityOnly: true})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { bus.Run(ctx) })
	wg.Go(func() { in.svc.RunPriority(sub) })
	stopRun := in.run()
	return bus, func() { cancel(); wg.Wait(); stopRun() }
}

func disabledUse(key string, ip string) events.Event {
	return events.Event{
		Time: time.Now(), Kind: events.KindBlocked, KeyID: key, ClientIP: netip.MustParseAddr(ip),
		Method: "POST", Path: "/v1/chat/completions", Status: 401, BlockedCode: "key_disabled", Priority: true,
	}
}

func TestTheUseOfADisabledKeyAlertsThroughThePriorityLane(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.withKey("abcd1234", "ci-bot")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(disabledUse("abcd1234", "203.0.113.7"))
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1", len(got))
		}
		for _, want := range []string{"HIGH", "ci-bot", "abcd1234", "203.0.113.7", "POST", "disabled", "kman key show abcd1234"} {
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("the alert lacks %q:\n%s", want, got[0].Body)
			}
		}
	})
}

func TestACanaryKeysUseIsCritical(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withKey("cafe0001", "decoy")
		bus, stop := in.priorityLane()
		defer stop()
		e := disabledUse("cafe0001", "198.51.100.4")
		e.BlockedCode = "invalid_key" // how the gateway answers a canary
		e.Canary = true
		bus.Emit(e)
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 || !strings.Contains(got[0].Body, "CRITICAL") || !strings.Contains(got[0].Body, "canary") {
			t.Fatalf("got %+v, want one critical canary alert", got)
		}
	})
}

func TestPriorityEventsThatAreNotAboutAKeyAreIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", true, "")
		bus, stop := in.priorityLane()
		defer stop()
		e := disabledUse("", "203.0.113.7")
		bus.Emit(e)
		e = disabledUse("abcd1234", "203.0.113.7")
		e.BlockedCode = "rate_limited"
		bus.Emit(e)
		synctest.Wait()
		if got := in.net.received("hooks.test"); len(got) != 0 {
			t.Errorf("got %+v, want nothing", got)
		}
	})
}

func TestRepeatedUseOfADisabledKeyIsThrottledAndCountedInTheNextAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t, func(o *alerts.Options) { o.ThrottleWindow = 10 * time.Minute })
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.withKey("abcd1234", "ci-bot")
		bus, stop := in.priorityLane()
		defer stop()
		for range 50 {
			bus.Emit(disabledUse("abcd1234", "203.0.113.7"))
		}
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 1 {
			t.Fatalf("%d alerts for 50 uses inside the window, want 1", n)
		}
		time.Sleep(11 * time.Minute)
		bus.Emit(disabledUse("abcd1234", "203.0.113.7"))
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 2 || !strings.Contains(got[1].Body, "49 more") {
			t.Fatalf("got %d alerts, want a second saying 49 more uses were throttled:\n%v", len(got), got)
		}
		// Another key is not throttled by this one.
		in.withKey("beef5678", "other")
		bus.Emit(disabledUse("beef5678", "203.0.113.7"))
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 3 {
			t.Errorf("%d alerts after another key's use, want 3", n)
		}
	})
}

// SPEC §9: an alert never carries request content, and the path is the caller's.
func TestAPriorityAlertNeverCarriesTheRequestPath(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", true, "")
		in.withKey("abcd1234", "ci-bot")
		in.withKey("cafe0001", "decoy")
		bus, stop := in.priorityLane()
		defer stop()
		hostile := "/v1/<b>x</b>/sk-ant-api03-FAKESECRET/*bold*"
		e := disabledUse("abcd1234", "203.0.113.7")
		e.Path = hostile
		bus.Emit(e)
		c := disabledUse("cafe0001", "203.0.113.8")
		c.Path, c.BlockedCode, c.Canary = hostile, "invalid_key", true
		c.Method = "EVIL*FAKESECRET*" // a method is a token the caller picks too
		bus.Emit(c)
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 2 {
			t.Fatalf("%d alerts, want 2", len(got))
		}
		for _, a := range got {
			for _, bad := range []string{"FAKESECRET", "<b>", "*bold*", "/v1/", "chat/completions"} {
				if strings.Contains(a.Body, bad) {
					t.Errorf("the alert carries the caller's path (%q):\n%s", bad, a.Body)
				}
			}
		}
		if !strings.Contains(got[0].Body, "POST") && !strings.Contains(got[1].Body, "POST") {
			t.Errorf("no alert names the method:\n%v", got)
		}
	})
}

// Only a real canary is critical: a key refused as invalid_key for another
// reason (an unknown status, a deleted credential) is not a canary.
func TestAnInvalidKeyRefusalThatIsNotACanaryDoesNotAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", true, "")
		in.withKey("abcd1234", "ci-bot")
		bus, stop := in.priorityLane()
		defer stop()
		e := disabledUse("abcd1234", "203.0.113.7")
		e.BlockedCode = "invalid_key"
		bus.Emit(e)
		synctest.Wait()
		if got := in.net.received("hooks.test"); len(got) != 0 {
			t.Errorf("got %+v, want nothing", got)
		}
	})
}

// withCanary adds a canary: a key of kind canary and its canary row.
func (in *install) withCanary(id, name, planted string, ignore ...string) {
	in.t.Helper()
	in.withKey(id, name)
	raw, _ := json.Marshal(ignore)
	if ignore == nil {
		raw = []byte(`[]`)
	}
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.CreateCanary(store.Canary{KeyID: id, Planted: planted, IgnoreJSON: string(raw), ArmedAt: t0})
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
}

func (in *install) canary(id string) store.Canary {
	in.t.Helper()
	var c store.Canary
	err := in.store.Do(context.Background(), func(tx *store.Tx) (err error) { c, err = tx.Canary(id); return })
	if err != nil {
		in.t.Fatal(err)
	}
	return c
}

func (in *install) resetCanary(id string) {
	in.t.Helper()
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		_, _, err := tx.ResetCanary(id, time.Now())
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
}

func canaryUse(key, ip string) events.Event {
	e := disabledUse(key, ip)
	e.BlockedCode, e.Canary = "invalid_key", true // how the gateway answers a canary
	return e
}

// SPEC §9, #87: a canary alert names the canary, where it was planted, and
// where the request came from: address, country, ASN and its label, and client.
func TestACanaryAlertNamesTheCanaryWhereItWasPlantedAndWhereTheRequestCameFrom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
		bus, stop := in.priorityLane()
		defer stop()
		e := canaryUse("cafe0001", "203.0.113.7")
		e.Country, e.ASN, e.ASOrg, e.Client = "DE", 3320, "Deutsche Telekom AG", "curl/8"
		bus.Emit(e)
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1", len(got))
		}
		for _, want := range []string{"CRITICAL", "canary", "decoy", "cafe0001", "the deploy repo's .env", "203.0.113.7", "DE",
			"AS3320", "Deutsche Telekom AG", "curl/8", "POST", "kman canary reset cafe0001", "kman key places cafe0001"} {
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("the alert lacks %q:\n%s", want, got[0].Body)
			}
		}
		c := in.canary("cafe0001")
		if c.FiredAt.IsZero() || c.Fires != 1 || c.Last.Country != "DE" || c.Last.ASN != 3320 || c.Last.Client != "curl/8" || c.Last.Address.String() != "203.0.113.7" {
			t.Errorf("the canary after its use = %+v, want it fired and holding what it saw", c)
		}
	})
}

// A request GeoIP could not place has no country or ASN, and the alert says what
// it knows and makes up nothing.
func TestACanaryAlertSaysOnlyWhatIsKnownOfWhereItCameFrom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the wiki")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1", len(got))
		}
		for _, bad := range []string{"Country:", "ASN:", "Client:", "AS0"} {
			if strings.Contains(got[0].Body, bad) {
				t.Errorf("the alert says %q for what it does not know:\n%s", bad, got[0].Body)
			}
		}
	})
}

// What the GeoIP database calls an AS is data from a file, so it is cleaned
// before it goes to a channel.
func TestACanaryAlertCleansTheLabelOfTheASN(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the wiki")
		bus, stop := in.priorityLane()
		defer stop()
		e := canaryUse("cafe0001", "203.0.113.7")
		e.ASN, e.ASOrg = 64500, "Evil\nCommands: rm -rf /"+strings.Repeat("x", 500)
		bus.Emit(e)
		synctest.Wait()
		body := in.net.received("hooks.test")[0].Body
		if strings.Contains(body, "\nCommands: rm") || strings.Count(body, "x") > 100 {
			t.Errorf("the alert carries the label as it was:\n%s", body)
		}
	})
}

func TestACanaryUsedFromAnIgnoredAddressDoesNotFireOrAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the wiki", "192.0.2.0/24", "198.51.100.7")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(canaryUse("cafe0001", "192.0.2.50"))
		bus.Emit(canaryUse("cafe0001", "198.51.100.7"))
		synctest.Wait()
		if got := in.net.received("hooks.test"); len(got) != 0 {
			t.Fatalf("an ignored address raised %d alerts: %+v", len(got), got)
		}
		if c := in.canary("cafe0001"); !c.FiredAt.IsZero() || c.Fires != 0 {
			t.Errorf("an ignored address fired the canary: %+v", c)
		}
		// The ignore list is the operator's scanners and nothing wider: an
		// address next to one on it is a stranger.
		bus.Emit(canaryUse("cafe0001", "198.51.100.8"))
		synctest.Wait()
		if got := in.net.received("hooks.test"); len(got) != 1 {
			t.Fatalf("%d alerts after a stranger's use, want 1", len(got))
		}
	})
}

// A canary is armed again by a reset, and its next use alerts at once, though
// it is inside the window that holds a fired canary's repeats to one alert. The
// address that fired it is not known to it either: nothing but the ignore list
// silences a canary (SPEC §4).
func TestTheFirstUseAfterAResetAlertsAtOnceFromTheSameAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t, func(o *alerts.Options) { o.ThrottleWindow = 10 * time.Minute })
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the wiki")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		synctest.Wait()
		in.resetCanary("cafe0001")
		bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		synctest.Wait()
		if got := in.net.received("hooks.test"); len(got) != 2 {
			t.Fatalf("%d alerts for a use, a reset and a use, want 2", len(got))
		}
		if c := in.canary("cafe0001"); c.Fires != 2 || c.FiredAt.IsZero() {
			t.Errorf("canary = %+v", c)
		}
	})
}

// While a canary is fired, more uses are counted and do not alert one by one: a
// leaked canary is a stream of requests, and an operator wants the first alert
// and then a count, not the stream. One alert per window, saying how many it held.
func TestUsesOfAFiredCanaryAreCountedAndAlertOncePerWindowWithTheCountHeldBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t, func(o *alerts.Options) { o.ThrottleWindow = 10 * time.Minute })
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withCanary("cafe0001", "decoy", "the wiki")
		in.withCanary("beef0002", "other", "the drive")
		bus, stop := in.priorityLane()
		defer stop()
		for range 50 {
			bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		}
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 1 {
			t.Fatalf("%d alerts for 50 uses of a fired canary, want 1", n)
		}
		if c := in.canary("cafe0001"); c.Fires != 50 || !c.LastFiredAt.Equal(time.Now()) {
			t.Errorf("canary = %+v, want every use counted", c)
		}
		time.Sleep(11 * time.Minute)
		bus.Emit(canaryUse("cafe0001", "203.0.113.9"))
		synctest.Wait()
		got := in.net.received("hooks.test")
		if len(got) != 2 || !strings.Contains(got[1].Body, "49 more") || !strings.Contains(got[1].Body, "203.0.113.9") {
			t.Fatalf("got %d alerts, want a second saying 49 more uses were held back:\n%v", len(got), got)
		}
		// Another canary is not held by this one.
		bus.Emit(canaryUse("beef0002", "203.0.113.7"))
		synctest.Wait()
		if n := len(in.net.received("hooks.test")); n != 3 {
			t.Errorf("%d alerts after another canary's use, want 3", n)
		}
	})
}

// A canary event for a key with no canary row (not made by kman canary) still
// alerts critical: it is a key that has no credential on purpose.
func TestACanaryKeyWithNoCanaryRowStillAlerts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", true, "")
		in.withKey("cafe0001", "decoy")
		bus, stop := in.priorityLane()
		defer stop()
		bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
		synctest.Wait()
		if got := in.net.received("hooks.test"); len(got) != 1 || !strings.Contains(got[0].Body, "CRITICAL") {
			t.Fatalf("got %+v, want one critical alert", got)
		}
	})
}

// A disabled key's alert says where the request came from, as a canary's does.
func TestADisabledKeyAlertSaysWhereTheRequestCameFromToo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		in.withKey("abcd1234", "ci-bot")
		bus, stop := in.priorityLane()
		defer stop()
		e := disabledUse("abcd1234", "203.0.113.7")
		e.Country, e.ASN, e.ASOrg, e.Client = "DE", 3320, "Deutsche Telekom AG", "openai-python/1"
		bus.Emit(e)
		synctest.Wait()
		body := in.net.received("hooks.test")[0].Body
		for _, want := range []string{"DE", "AS3320", "Deutsche Telekom AG", "openai-python/1"} {
			if !strings.Contains(body, want) {
				t.Errorf("the alert lacks %q:\n%s", want, body)
			}
		}
	})
}

// The canary deadline gate itself is in e2e, on the real binary and the real
// clock (TestCanaryDeadlineGate). This is its logic on a fake one: with 256
// clients emitting events through the bus into the store writer, and a channel
// that never answers, a canary's alert and a disabled key's reach the healthy
// channel with no time having passed at all, so nothing in the path waits on a
// timer, a batch or the hung channel.
func TestCanaryAndDisabledKeyAlertsWaitOnNothingUnder256WayLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_hung", "slack", "hung.test", "low", true, "")
		in.channel("ch_ok", "slack", "ok.test", "low", true, "")
		in.net.on("hung.test", hang)
		in.withCanary("cafe0001", "decoy", "the deploy repo's .env")
		in.withKey("abcd1234", "ci-bot")

		bus := events.New(events.Options{})
		writer := events.NewWriter(events.WriterOptions{Sink: events.SQLite(in.store)})
		storeSub := bus.Subscribe("store", events.SubOptions{Buffer: 4 * events.DefaultCapacity})
		alertSub := bus.Subscribe("alerts", events.SubOptions{PriorityOnly: true, Buffer: events.DefaultPriorityCapacity})
		ctx, cancel := context.WithCancel(context.Background())
		var pipeline sync.WaitGroup
		pipeline.Go(func() { bus.Run(ctx) })
		pipeline.Go(func() { writer.Run(storeSub) })
		pipeline.Go(func() { in.svc.RunPriority(alertSub) })
		stopDelivery := in.run()
		defer func() { cancel(); pipeline.Wait(); stopDelivery() }()

		start := time.Now()
		var clients sync.WaitGroup
		for i := range 256 {
			clients.Go(func() {
				ip := netip.AddrFrom4([4]byte{10, 0, byte(i / 250), byte(i%250 + 1)})
				for range 20 {
					bus.Emit(events.Event{Time: time.Now(), Kind: events.KindRequest, KeyID: "load0001", ClientIP: ip, Status: 200, Method: "POST", Path: "/v1/chat/completions"})
				}
				if i == 100 {
					bus.Emit(canaryUse("cafe0001", "203.0.113.7"))
				}
				if i == 200 {
					bus.Emit(disabledUse("abcd1234", "203.0.113.8"))
				}
			})
		}
		clients.Wait()
		synctest.Wait()
		if got := time.Since(start); got != 0 {
			t.Errorf("the alerts took %v of fake time: something in the path waited", got)
		}
		var canary, disabled bool
		for _, r := range in.net.received("ok.test") {
			canary = canary || (strings.Contains(r.Body, "A canary key was used") && strings.Contains(r.Body, "cafe0001"))
			disabled = disabled || (strings.Contains(r.Body, "A disabled key was used") && strings.Contains(r.Body, "abcd1234"))
		}
		if !canary || !disabled {
			t.Errorf("the healthy channel got canary=%v, disabled key=%v of %d alerts", canary, disabled, len(in.net.received("ok.test")))
		}
		if c := in.outbox("ch_hung"); c.Pending < 2 {
			t.Errorf("the hung channel's outbox = %+v, want the alerts held for it", c)
		}
		if s := bus.Stats(); s.DroppedPriority != 0 {
			t.Errorf("the priority lane dropped %d events", s.DroppedPriority)
		}
	})
}
