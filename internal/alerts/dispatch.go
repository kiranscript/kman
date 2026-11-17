package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// Run delivers the outbox until ctx ends. Each live channel has a goroutine of
// its own, so a channel that hangs or fails holds up no other (SPEC §9). What
// was left in the outbox by an earlier run is delivered first.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	workers := map[string]*worker{}
	defer func() {
		for _, w := range workers {
			w.cancel()
		}
		wg.Wait()
	}()
	timer := time.NewTimer(rescanEvery)
	defer timer.Stop()
	for {
		s.reconcile(ctx, &wg, workers)
		for _, w := range workers {
			w.poke()
		}
		timer.Reset(rescanEvery)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		}
	}
}

// worker delivers one channel's alerts.
type worker struct {
	cancel context.CancelFunc
	wake   chan struct{}
}

func (w *worker) poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// reconcile starts a worker for each live channel that has none, and stops the
// worker of each channel that is gone.
func (s *Service) reconcile(ctx context.Context, wg *sync.WaitGroup, workers map[string]*worker) {
	var chs []store.AlertChannel
	err := s.o.Store.Do(ctx, func(tx *store.Tx) (err error) { chs, err = tx.AlertChannels(); return })
	if err != nil {
		if ctx.Err() == nil {
			s.o.Log.Error("alerts: could not list the channels", "err", err)
		}
		return
	}
	live := map[string]bool{}
	for _, c := range chs {
		live[c.ID] = true
		if workers[c.ID] != nil {
			continue
		}
		wctx, cancel := context.WithCancel(ctx)
		w := &worker{cancel: cancel, wake: make(chan struct{}, 1)}
		workers[c.ID] = w
		wg.Go(func() { s.work(wctx, c, w) })
	}
	for id, w := range workers {
		if !live[id] {
			w.cancel()
			delete(workers, id)
		}
	}
}

// work delivers what is due for c, then sleeps until the next alert is due, or
// until it is poked.
func (s *Service) work(ctx context.Context, c store.AlertChannel, w *worker) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		wait := rescanEvery
		var due []store.OutboxAlert
		err := s.o.Store.Do(ctx, func(tx *store.Tx) (err error) { due, err = tx.DueAlerts(c.ID, s.o.Now(), batch); return })
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			s.o.Log.Error("alerts: could not read the outbox", "channel", c.Name, "err", err)
		case len(due) > 0:
			for _, a := range due {
				if ctx.Err() != nil {
					return
				}
				s.deliver(ctx, c, a)
			}
			if len(due) == batch {
				continue
			}
		}
		_ = s.o.Store.Do(ctx, func(tx *store.Tx) error {
			next, ok, err := tx.NextAlertDue(c.ID)
			if ok && err == nil {
				wait = min(max(next.Sub(s.o.Now()), 0), rescanEvery)
			}
			return err
		})
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-timer.C:
		}
	}
}

// deliver makes one attempt at one alert and records what came of it.
func (s *Service) deliver(ctx context.Context, c store.AlertChannel, d store.OutboxAlert) {
	var a Alert
	if err := json.Unmarshal([]byte(d.PayloadJSON), &a); err != nil {
		s.record(ctx, d, outcome{permanent: true, reason: "the alert in the outbox cannot be read"})
		return
	}
	out := s.send(ctx, c, a, strconv.FormatInt(d.ID, 10))
	if ctx.Err() != nil {
		return // stopping: the alert stays in the outbox for the next run
	}
	s.record(ctx, d, out)
}

// outcome is what one attempt came to.
type outcome struct {
	ok         bool
	permanent  bool          // retrying would change nothing
	retryAfter time.Duration // what the channel asked for, if it did
	reason     string        // never holds the channel's address
}

// record stores an attempt's outcome: delivered, to be retried after a backoff,
// or given up.
func (s *Service) record(ctx context.Context, d store.OutboxAlert, out outcome) {
	now := s.o.Now()
	err := s.o.Store.Do(ctx, func(tx *store.Tx) error {
		switch {
		case out.ok:
			return tx.AlertDelivered(d.ID, now)
		case out.permanent:
			return tx.AlertFailed(d.ID, now, out.reason)
		case now.Sub(d.CreatedAt) >= s.o.GiveUpAfter:
			return tx.AlertFailed(d.ID, now, fmt.Sprintf("given up after %s: %s", s.o.GiveUpAfter, out.reason))
		}
		return tx.AlertRetry(d.ID, now.Add(max(s.o.Backoff(d.Attempts+1), out.retryAfter)), out.reason)
	})
	if err != nil && ctx.Err() == nil {
		s.o.Log.Error("alerts: could not record an attempt", "alert", d.ID, "err", err)
	}
}

// send makes the request for a to channel c.
func (s *Service) send(ctx context.Context, c store.AlertChannel, a Alert, deliveryID string) outcome {
	var cfg store.AlertChannelConfig
	raw, ok := s.o.Secret(c.ID)
	if !ok || json.Unmarshal(raw, &cfg) != nil || cfg.URL == "" {
		return outcome{reason: "the channel's address is not loaded"}
	}
	link := s.link(a)
	var body []byte
	var err error
	headers := http.Header{"Content-Type": {"application/json"}}
	switch c.Kind {
	case "slack":
		body, err = slackBody(a, link)
	case "webhook":
		if body, err = webhookBody(a, deliveryID, link); err == nil {
			ts, sig := sign(cfg.SigningSecret, s.o.Now(), body)
			headers.Set("X-Kman-Timestamp", ts)
			headers.Set("X-Kman-Signature", sig)
			headers.Set("X-Kman-Delivery", deliveryID)
		}
	default:
		return outcome{permanent: true, reason: "unknown channel kind " + strconv.Quote(c.Kind)}
	}
	if err != nil {
		return outcome{permanent: true, reason: "the alert cannot be rendered"}
	}
	ctx, cancel := context.WithTimeout(ctx, s.o.DeliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return outcome{permanent: true, reason: "the channel's address is not usable"}
	}
	req.Header = headers
	req.Header.Set("User-Agent", "kman")
	resp, err := s.o.Client.Do(req)
	if err != nil {
		return outcome{reason: transportReason(err, s.o.DeliveryTimeout)}
	}
	defer resp.Body.Close()
	return classify(resp)
}

// link is the deep link to an alert's incident, only when dashboard.url is set.
func (s *Service) link(a Alert) string {
	if s.o.DashboardURL == "" || a.IncidentID == 0 {
		return ""
	}
	return strings.TrimRight(s.o.DashboardURL, "/") + "/ui/incidents/" + strconv.FormatInt(a.IncidentID, 10)
}

// classify turns a response into an outcome. A channel that is busy or broken
// is tried again; one that refuses the alert is not, since the same alert would
// be refused again.
func classify(resp *http.Response) outcome {
	code := resp.StatusCode
	reason := "the channel answered " + strconv.Itoa(code) + " " + http.StatusText(code)
	switch {
	case code >= 200 && code < 300:
		return outcome{ok: true}
	case code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500:
		out := outcome{reason: reason}
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			out.retryAfter = time.Duration(secs) * time.Second
		}
		return out
	}
	return outcome{permanent: true, reason: reason}
}

// transportReason says why a request failed without the address net/http
// puts in its errors: a Slack address is a secret.
func transportReason(err error, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "no answer within " + timeout.String()
	}
	if u, ok := errors.AsType[*url.Error](err); ok {
		err = u.Err
	}
	if op, ok := errors.AsType[*net.OpError](err); ok {
		return "could not reach the channel: " + op.Op + ": " + errText(op.Err)
	}
	return "could not reach the channel: " + errText(err)
}

func errText(err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "no such host"
	}
	return err.Error()
}

// Test sends one test alert to a channel at once, whatever its minimum
// severity, and says why it failed if it did. It does not use the outbox, so
// it works with Run stopped, and nothing is retried.
func (s *Service) Test(ctx context.Context, channelID string) error {
	var c store.AlertChannel
	if err := s.o.Store.Do(ctx, func(tx *store.Tx) (err error) { c, err = tx.AlertChannel(channelID); return }); err != nil {
		return err
	}
	out := s.send(ctx, c, Alert{
		Kind: "test", Severity: Low, Title: "kman test alert", Time: s.o.Now(),
		Evidence: []Fact{{Label: "Channel", Value: c.Name}},
		Commands: []string{"kman channel list"},
	}, "test")
	if out.ok {
		return nil
	}
	return errors.New(out.reason)
}
