package alerts_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/store"
)

func TestAChannelAddedWhileRunningGetsAlertsAndARemovedOneStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		stop := in.run()
		defer stop()
		synctest.Wait()
		in.channel("ch_new", "slack", "new.test", "low", true, "")
		in.svc.Wake()
		in.svc.Notify(context.Background(), sample())
		synctest.Wait()
		if n := len(in.net.received("new.test")); n != 1 {
			t.Fatalf("the new channel got %d alerts, want 1", n)
		}
		err := in.store.Do(context.Background(), func(tx *store.Tx) error { return tx.DeleteAlertChannel("ch_new", time.Now()) })
		if err != nil {
			t.Fatal(err)
		}
		in.svc.Wake()
		in.svc.Notify(context.Background(), sample())
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := len(in.net.received("new.test")); n != 1 {
			t.Errorf("a removed channel got %d alerts, want it to stay at 1", n)
		}
	})
}

func TestARemovedChannelsHungAttemptIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t, func(o *alerts.Options) { o.DeliveryTimeout = time.Hour })
		in.channel("ch_1", "slack", "hung.test", "low", true, "")
		in.net.on("hung.test", hang)
		stop := in.run()
		in.svc.Notify(context.Background(), sample())
		synctest.Wait() // the worker is stuck in its attempt
		err := in.store.Do(context.Background(), func(tx *store.Tx) error { return tx.DeleteAlertChannel("ch_1", time.Now()) })
		if err != nil {
			t.Fatal(err)
		}
		in.svc.Wake()
		synctest.Wait() // the worker has dropped its hour-long attempt
		start := time.Now()
		stop()
		if d := time.Since(start); d != 0 {
			t.Errorf("stopping took %v", d)
		}
	})
}

func TestTestSendsOneAlertNowToTheNamedChannelWhateverItsMinimumSeverity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "critical", false, "")
		// No dispatcher is running: a test does not go through the outbox.
		if err := in.svc.Test(context.Background(), "ch_1"); err != nil {
			t.Fatalf("Test: %v", err)
		}
		got := in.net.received("hooks.test")
		if len(got) != 1 || !strings.Contains(got[0].Body, "test") {
			t.Fatalf("slack was sent %+v, want one test message", got)
		}
		if c := in.outbox("ch_1"); c.Pending+c.Delivered+c.Failed != 0 {
			t.Errorf("the test left %+v in the outbox", c)
		}
	})
}

func TestTestSaysWhyAChannelFailedAndNeverQuotesItsAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "gone.test", "low", true, "")
		in.net.on("gone.test", func(*http.Request) (*http.Response, error) { return status(404), nil })
		err := in.svc.Test(context.Background(), "ch_1")
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("Test = %v, want an error naming the 404", err)
		}
		if strings.Contains(err.Error(), "TOKEN") || strings.Contains(err.Error(), "https://") {
			t.Errorf("the error quotes the address: %v", err)
		}
		in.net.on("gone.test", hang)
		err = in.svc.Test(context.Background(), "ch_1")
		if err == nil || !strings.Contains(err.Error(), "no answer within 10s") {
			t.Errorf("Test of a hung channel = %v, want a timeout after the delivery timeout", err)
		}
	})
}

func TestAnAlertLinksToItsIncidentOnlyWhenTheDashboardURLIsSet(t *testing.T) {
	a := sample()
	a.IncidentID = 17
	for _, c := range []struct {
		name, dashboard string
		want            bool
	}{{"unset", "", false}, {"set", "https://kman.example.com/", true}} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				in := newInstall(t, func(o *alerts.Options) { o.DashboardURL = c.dashboard })
				in.channel("ch_s", "slack", "slack.test", "low", true, "")
				in.channel("ch_w", "webhook", "hook.test", "low", true, "kmwhsec_x")
				in.svc.Notify(context.Background(), a)
				stop := in.run()
				defer stop()
				synctest.Wait()
				for _, host := range []string{"slack.test", "hook.test"} {
					got := in.net.received(host)
					if len(got) != 1 {
						t.Fatalf("%s got %d alerts", host, len(got))
					}
					has := strings.Contains(got[0].Body, "https://kman.example.com/ui/incidents/17")
					if has != c.want {
						t.Errorf("%s: link present = %v, want %v:\n%s", host, has, c.want, got[0].Body)
					}
					if strings.Contains(got[0].Body, "http") && !c.want {
						t.Errorf("%s: the alert has a link with no dashboard.url:\n%s", host, got[0].Body)
					}
				}
			})
		})
	}
}
