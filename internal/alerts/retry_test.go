package alerts_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/store"
)

// answers makes a host answer with these statuses in turn, then 200.
func answers(codes ...int) func(*http.Request) (*http.Response, error) {
	var n atomic.Int32
	return func(*http.Request) (*http.Response, error) {
		i := int(n.Add(1)) - 1
		if i < len(codes) {
			return status(codes[i]), nil
		}
		return status(200), nil
	}
}

func TestAFailedAlertIsRetriedWithABackoffUntilItIsDelivered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "flaky.test", "low", true, "")
		in.net.on("flaky.test", answers(503, 502))
		start := time.Now()
		stop := in.run()
		defer stop()
		if err := in.svc.Notify(context.Background(), sample()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		got := in.net.received("flaky.test")
		if len(got) != 3 {
			t.Fatalf("%d attempts, want 3 (two failures, then success)", len(got))
		}
		// The default backoff is 5 s, then 10 s.
		if d := got[1].At.Sub(got[0].At); d != 5*time.Second {
			t.Errorf("the second attempt came %v after the first, want 5s", d)
		}
		if d := got[2].At.Sub(got[1].At); d != 10*time.Second {
			t.Errorf("the third attempt came %v after the second, want 10s", d)
		}
		if got[0].At.Sub(start) > time.Second {
			t.Errorf("the first attempt waited %v", got[0].At.Sub(start))
		}
		if c := in.outbox("ch_1"); c.Delivered != 1 || c.Pending != 0 || c.Failed != 0 {
			t.Errorf("outbox = %+v, want delivered", c)
		}
	})
}

func TestARetryAfterLongerThanTheBackoffIsHonoured(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "busy.test", "low", true, "")
		var n atomic.Int32
		in.net.on("busy.test", func(*http.Request) (*http.Response, error) {
			if n.Add(1) == 1 {
				r := status(429)
				r.Header.Set("Retry-After", "60")
				return r, nil
			}
			return status(200), nil
		})
		stop := in.run()
		defer stop()
		in.svc.Notify(context.Background(), sample())
		time.Sleep(time.Hour)
		synctest.Wait()
		got := in.net.received("busy.test")
		if len(got) != 2 || got[1].At.Sub(got[0].At) != 60*time.Second {
			t.Fatalf("attempts at %v, want two, 60s apart", got)
		}
	})
}

func TestAnAlertTheChannelRefusesIsGivenUpAtOnceAndNeverQuotesTheAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "gone.test", "low", true, "")
		in.net.on("gone.test", answers(404))
		stop := in.run()
		defer stop()
		in.svc.Notify(context.Background(), sample())
		time.Sleep(time.Hour)
		synctest.Wait()
		if n := len(in.net.received("gone.test")); n != 1 {
			t.Errorf("%d attempts at an alert the channel refused, want 1", n)
		}
		c := in.outbox("ch_1")
		if c.Failed != 1 || c.Pending != 0 || !strings.Contains(c.LastError, "404") {
			t.Errorf("outbox = %+v, want it given up with the 404", c)
		}
	})
}

func TestAFailureNeverRecordsTheChannelsAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "down.test", "low", true, "")
		in.net.on("down.test", func(r *http.Request) (*http.Response, error) {
			// net/http wraps a transport's error in one that names the address.
			return nil, errors.New("connection refused")
		})
		stop := in.run()
		defer stop()
		in.svc.Notify(context.Background(), sample())
		synctest.Wait()
		c := in.outbox("ch_1")
		if c.Pending != 1 || c.LastError == "" {
			t.Fatalf("outbox = %+v, want a pending alert with its error", c)
		}
		if strings.Contains(c.LastError, "TOKEN") || strings.Contains(c.LastError, "down.test") || strings.Contains(c.LastError, "https://") {
			t.Errorf("the recorded error quotes the address: %q", c.LastError)
		}
	})
}

func TestAnAlertIsGivenUpAfterTheRetryWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t, func(o *alerts.Options) { o.GiveUpAfter = time.Hour })
		in.channel("ch_1", "slack", "down.test", "low", true, "")
		in.net.on("down.test", func(*http.Request) (*http.Response, error) { return status(503), nil })
		stop := in.run()
		defer stop()
		in.svc.Notify(context.Background(), sample())
		time.Sleep(3 * time.Hour)
		synctest.Wait()
		c := in.outbox("ch_1")
		if c.Failed != 1 || c.Pending != 0 || !strings.Contains(c.LastError, "given up") {
			t.Fatalf("outbox = %+v, want the alert given up after the window", c)
		}
		before := len(in.net.received("down.test"))
		time.Sleep(time.Hour)
		synctest.Wait()
		if after := len(in.net.received("down.test")); after != before {
			t.Errorf("attempts continued after the alert was given up: %d, then %d", before, after)
		}
	})
}

func TestAnAlertSurvivesARestartBecauseTheOutboxIsPersisted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", true, "")
		// kman accepts an alert, then stops before delivering it.
		if err := in.svc.Notify(context.Background(), sample()); err != nil {
			t.Fatal(err)
		}
		if n := len(in.net.received("hooks.test")); n != 0 {
			t.Fatalf("%d sent with no dispatcher running", n)
		}
		// A later run, with a service of its own over the same store, delivers it.
		again := alerts.New(alerts.Options{Store: in.store, Now: time.Now, Client: &http.Client{Transport: in.net},
			Secret: func(id string) ([]byte, bool) { b, ok := in.secrets[id]; return b, ok }})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { again.Run(ctx); close(done) }()
		synctest.Wait()
		cancel()
		<-done
		if n := len(in.net.received("hooks.test")); n != 1 {
			t.Errorf("after the restart %d were sent, want the 1 that was waiting", n)
		}
	})
}

func TestAnAlertHeldWhileShuttingDownStaysInTheOutbox(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hung.test", "low", true, "")
		in.net.on("hung.test", hang)
		stop := in.run()
		in.svc.Notify(context.Background(), sample())
		synctest.Wait()
		stop() // the attempt is cancelled, which says nothing about the channel
		var c store.OutboxCount
		c = in.outbox("ch_1")
		if c.Pending != 1 || c.Failed != 0 || c.Delivered != 0 {
			t.Errorf("outbox = %+v, want the alert still pending", c)
		}
	})
}

// TestRegression_F7_AHungChannelNeitherDropsNorDelaysOtherChannelsAlerts is
// F7, the lossy alert pipeline: keman's single delivery loop meant one channel
// that hung held up every other, and a full queue dropped alerts. Here every
// channel has its own worker and the outbox is persisted.
func TestRegression_F7_AHungChannelNeitherDropsNorDelaysOtherChannelsAlerts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_hung", "slack", "hung.test", "low", true, "")
		in.channel("ch_ok", "slack", "ok.test", "low", true, "")
		in.channel("ch_hook", "webhook", "hook.test", "low", true, "kmwhsec_x")
		in.net.on("hung.test", hang)
		start := time.Now()
		stop := in.run()
		defer stop()
		const n = 300 // more than a worker's batch
		for i := range n {
			a := sample()
			a.Priority = i%7 == 0
			if err := in.svc.Notify(context.Background(), a); err != nil {
				t.Fatal(err)
			}
		}
		// The healthy channels have everything while the hung one is still stuck
		// on its first alert, with no time having passed at all.
		synctest.Wait()
		if got := time.Since(start); got != 0 {
			t.Errorf("the healthy channels took %v to be caught up", got)
		}
		for _, host := range []string{"ok.test", "hook.test"} {
			if got := len(in.net.received(host)); got != n {
				t.Errorf("%s got %d alerts, want all %d", host, got, n)
			}
		}
		if c := in.outbox("ch_hung"); c.Pending != n {
			t.Errorf("the hung channel's alerts: %+v, want %d still held, none dropped", c, n)
		}
		// When the hung channel comes back it gets every alert too.
		in.net.on("hung.test", nil)
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := len(in.net.received("hung.test")); got < n {
			t.Errorf("the recovered channel was sent %d requests, want at least %d", got, n)
		}
		if c := in.outbox("ch_hung"); c.Delivered != n || c.Pending != 0 || c.Failed != 0 {
			t.Errorf("after recovery: %+v, want all %d delivered", c, n)
		}
	})
}
