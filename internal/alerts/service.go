package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// Delivery's defaults.
const (
	// DefaultDeliveryTimeout bounds one attempt to reach a channel, so that a
	// channel that hangs ties up only its own goroutine, and only for this long.
	DefaultDeliveryTimeout = 10 * time.Second
	// DefaultGiveUpAfter is how long an alert is retried before it is given up.
	DefaultGiveUpAfter = 24 * time.Hour
	// DefaultThrottleWindow is how often one key's use alerts.
	DefaultThrottleWindow = 10 * time.Minute
	// rescanEvery is the longest a worker or Run sleeps with nothing due: the
	// outbox and the channels are read again at least this often.
	rescanEvery = 30 * time.Second
	// batch is how many due alerts a worker reads at a time.
	batch = 50
)

// Options configures New.
type Options struct {
	Store *store.Store
	// Secret returns a channel's sealed config in plaintext (a JSON
	// store.AlertChannelConfig): pass the vault's Credential, which admin fills
	// when it seals a channel. The slice is the vault's: it is read at once and
	// never kept.
	Secret func(channelID string) ([]byte, bool)
	// Now is the clock: alerts never reads the wall clock itself.
	Now func() time.Time
	// Client sends to the channels. It defaults to one that follows no
	// redirects: a channel's address is the operator's, and an alert is not
	// sent anywhere else.
	Client *http.Client
	// DeliveryTimeout bounds one attempt. Default DefaultDeliveryTimeout.
	DeliveryTimeout time.Duration
	// GiveUpAfter is how long an alert is retried before it is given up.
	// Default DefaultGiveUpAfter.
	GiveUpAfter time.Duration
	// Backoff is how long to wait after the attempts-th failed attempt, from 1.
	// Default: 5 s, doubling, at most 15 min.
	Backoff func(attempts int) time.Duration
	// ThrottleWindow is how long a key's repeated use, which alerts through the
	// priority lane, is held to one alert. Default DefaultThrottleWindow.
	ThrottleWindow time.Duration
	// DashboardURL is dashboard.url from kman.yaml. Only when it is set does an
	// alert carry a link, and then only to its incident's page.
	DashboardURL string
	Log          *slog.Logger // defaults to discarding
}

// Service is the alert outbox and the delivery of what it holds.
type Service struct {
	o    Options
	wake chan struct{}
}

// New returns a Service. Nothing is delivered until Run.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Client == nil {
		o.Client = noRedirects(&http.Client{})
	}
	if o.DeliveryTimeout <= 0 {
		o.DeliveryTimeout = DefaultDeliveryTimeout
	}
	if o.GiveUpAfter <= 0 {
		o.GiveUpAfter = DefaultGiveUpAfter
	}
	if o.ThrottleWindow <= 0 {
		o.ThrottleWindow = DefaultThrottleWindow
	}
	if o.Backoff == nil {
		o.Backoff = defaultBackoff
	}
	return &Service{o: o, wake: make(chan struct{}, 1)}
}

func defaultBackoff(attempts int) time.Duration {
	d := 5 * time.Second
	for range min(attempts-1, 10) {
		d *= 2
	}
	return min(d, 15*time.Minute)
}

// noRedirects makes c answer a redirect with the redirect, which delivery
// treats as a failure, instead of sending the alert on.
func noRedirects(c *http.Client) *http.Client {
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// Wake tells Run that the outbox or the channels changed. Call it after the
// transaction of a NotifyTx, or of a change to a channel, has committed.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Notify puts a in the outbox for every channel that takes it, in its own
// transaction, and wakes the dispatcher.
func (s *Service) Notify(ctx context.Context, a Alert) error {
	if err := s.o.Store.Do(ctx, func(tx *store.Tx) error { return s.NotifyTx(tx, a) }); err != nil {
		return err
	}
	s.Wake()
	return nil
}

// NotifyTx is Notify in the caller's transaction, so an alert and the change
// that raised it commit together or not at all. The caller wakes the
// dispatcher (Wake) once the transaction has committed.
func (s *Service) NotifyTx(tx *store.Tx, a Alert) error {
	chs, err := tx.AlertChannels()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("alerts: marshal alert: %w", err)
	}
	for _, c := range chs {
		if !takes(c, a) {
			continue
		}
		_, err := tx.EnqueueAlert(store.OutboxAlert{
			ChannelID: c.ID, Kind: a.Kind, IncidentID: a.IncidentID, Priority: a.Priority,
			PayloadJSON: string(payload), CreatedAt: s.o.Now(),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// takes reports whether channel c is meant to get alert a: it is at or above
// the channel's minimum severity, and a warning only if the channel takes them.
func takes(c store.AlertChannel, a Alert) bool {
	min, err := ParseSeverity(c.MinSeverity)
	if err != nil {
		min = Medium // a minimum this kman does not know is the default, not "everything"
	}
	if a.Severity < min {
		return false
	}
	return !a.Warning || c.LifecycleAlerts
}
