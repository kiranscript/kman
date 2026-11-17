package alerts_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
)

func TestAnAlertReachesASlackChannelWithTheKeyEvidenceAndCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.slack.test", "medium", true, "")
		stop := in.run()
		defer stop()
		if err := in.svc.Notify(context.Background(), sample()); err != nil {
			t.Fatalf("Notify: %v", err)
		}
		synctest.Wait()
		got := in.net.received("hooks.slack.test")
		if len(got) != 1 {
			t.Fatalf("slack was sent %d messages, want 1", len(got))
		}
		if got[0].Path != "/hook/TOKEN-ch_1" || got[0].Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %+v", got[0])
		}
		for _, want := range []string{"HIGH", "ci-bot", "abcd1234", "Actor", "owner", "kman key enable abcd1234"} {
			if !strings.Contains(got[0].Body, want) {
				t.Errorf("the message lacks %q:\n%s", want, got[0].Body)
			}
		}
		if c := in.outbox("ch_1"); c.Delivered != 1 || c.Pending != 0 {
			t.Errorf("outbox = %+v, want it delivered", c)
		}
	})
}

func TestAWebhookGetsJSONSignedWithTheChannelsSecret(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "webhook", "hooks.example.test", "medium", true, "kmwhsec_s3cret")
		stop := in.run()
		defer stop()
		if err := in.svc.Notify(context.Background(), sample()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		got := in.net.received("hooks.example.test")
		if len(got) != 1 {
			t.Fatalf("the webhook was sent %d requests, want 1", len(got))
		}
		r := got[0]
		// The signature is what a receiver computes: HMAC-SHA256 of
		// "<timestamp>.<body>" under the signing secret, in hex.
		ts := r.Header.Get("X-Kman-Timestamp")
		mac := hmac.New(sha256.New, []byte("kmwhsec_s3cret"))
		mac.Write([]byte(ts + "." + r.Body))
		if want := "v1=" + hex.EncodeToString(mac.Sum(nil)); r.Header.Get("X-Kman-Signature") != want {
			t.Errorf("X-Kman-Signature = %q, want %q", r.Header.Get("X-Kman-Signature"), want)
		}
		if secs, err := strconv.ParseInt(ts, 10, 64); err != nil || secs != time.Now().Unix() {
			t.Errorf("X-Kman-Timestamp = %q, want the time of sending in unix seconds (%d)", ts, time.Now().Unix())
		}
		if r.Header.Get("X-Kman-Delivery") == "" {
			t.Error("no X-Kman-Delivery: a receiver needs it to drop a duplicate after a retry")
		}
		var body struct {
			Version  int    `json:"version"`
			Severity string `json:"severity"`
			Title    string `json:"title"`
			Key      struct{ ID, Name string }
			Evidence []struct{ Label, Value string }
			Commands []string
		}
		if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
			t.Fatalf("the body is not JSON: %v\n%s", err, r.Body)
		}
		if body.Version != 1 || body.Severity != "high" || body.Key.ID != "abcd1234" || body.Key.Name != "ci-bot" ||
			len(body.Evidence) != 2 || len(body.Commands) != 2 || body.Title == "" {
			t.Errorf("body = %+v", body)
		}
		if strings.Contains(r.Body, "kmwhsec_s3cret") || strings.Contains(r.Body, "TOKEN-ch_1") {
			t.Errorf("the body holds the signing secret or the address:\n%s", r.Body)
		}
	})
}

func TestAChannelTakesAlertsAtItsMinimumSeverityAndAbove(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_low", "slack", "low.test", "low", true, "")
		in.channel("ch_high", "slack", "high.test", "high", true, "")
		stop := in.run()
		defer stop()
		for _, sev := range []alerts.Severity{alerts.Low, alerts.Medium, alerts.High, alerts.Critical} {
			a := sample()
			a.Severity = sev
			if err := in.svc.Notify(context.Background(), a); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if n := len(in.net.received("low.test")); n != 4 {
			t.Errorf("the low channel got %d alerts, want 4", n)
		}
		if n := len(in.net.received("high.test")); n != 2 {
			t.Errorf("the high channel got %d alerts, want the 2 at high or above", n)
		}
	})
}

func TestSpendAndLifecycleWarningsFollowTheChannelsToggle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_on", "slack", "on.test", "low", true, "")
		in.channel("ch_off", "slack", "off.test", "low", false, "")
		stop := in.run()
		defer stop()
		warn := sample()
		warn.Kind, warn.Warning = "soft_budget", true
		if err := in.svc.Notify(context.Background(), warn); err != nil {
			t.Fatal(err)
		}
		if err := in.svc.Notify(context.Background(), sample()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if n := len(in.net.received("on.test")); n != 2 {
			t.Errorf("the channel with warnings on got %d, want 2", n)
		}
		if n := len(in.net.received("off.test")); n != 1 {
			t.Errorf("the channel with warnings off got %d, want only the audit alert", n)
		}
	})
}
