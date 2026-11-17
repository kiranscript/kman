package alerts_test

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/kirank55/kman/internal/alerts"
)

// refuses fails the test on any request: it is the network, which kman does not
// touch unless an operator configured a channel (ADR 0008).
type refuses struct{ t *testing.T }

func (r refuses) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("alerts made a network call to %s with no channel configured", req.URL.Host)
	return nil, http.ErrNotSupported
}

func TestWithNoChannelConfiguredNoAlertTouchesTheNetwork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t, func(o *alerts.Options) { o.Client = &http.Client{Transport: refuses{t}} })
		in.withKey("abcd1234", "ci-bot")
		bus, stop := in.priorityLane()
		defer stop()
		if err := in.svc.Notify(context.Background(), sample()); err != nil {
			t.Fatal(err)
		}
		bus.Emit(disabledUse("abcd1234", "203.0.113.7"))
		synctest.Wait()
	})
}
