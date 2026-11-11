package alerts_test

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/events"
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
		for _, want := range []string{"HIGH", "ci-bot", "abcd1234", "203.0.113.7", "POST /v1/chat/completions", "disabled", "kman key show abcd1234"} {
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
