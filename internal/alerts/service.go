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
	// DefaultWatchEvery is how often spend and key expiry are checked: a spend
	// warning follows the spend by at most this and the 5 s flush.
	DefaultWatchEvery = time.Minute
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
	// Limits reads a key's policy as the amounts its spend warnings count from.
	// The composition root fills it from the policy module, which alerts may not
	// import (SPEC §2). Without it no key has a spend warning.
	Limits func(policyJSON string) (Limits, error)
	// Settings returns the install's settings that spend warnings read, at each
	// check. The composition root fills it from admin's settings (alerts may not
	// import it). Without it, or when it fails, the defaults apply.
	Settings func(context.Context) (Settings, error)
	// CredentialWarningWindow is how long an upstream's refused credential is held
	// to one warning. Default DefaultCredentialWarningWindow.
	CredentialWarningWindow time.Duration
	// WatchEvery is how often RunWatch checks spend and expiry. Default
	// DefaultWatchEvery.
	WatchEvery time.Duration
	Log        *slog.Logger // defaults to discarding
}

// Settings are the install's settings that spend warnings read (SPEC §3).
type Settings struct {
	// SoftBudgets is the soft_budget_defaults setting: the percentages of a cap
	// or soft budget that warn for a key that sets none. Empty is "none": only a
	// key's own percentages warn.
	SoftBudgets []int
	// Location is the spend_timezone: a day and a month begin at 00:00 in it, as
	// the meter counts them. Nil is UTC.
	Location *time.Location
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
	if o.CredentialWarningWindow <= 0 {
		o.CredentialWarningWindow = DefaultCredentialWarningWindow
	}
	if o.WatchEvery <= 0 {
		o.WatchEvery = DefaultWatchEvery
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
	_, err := s.enqueue(tx, a)
	return err
}

// enqueue is NotifyTx that says how many channels the alert was queued for.
func (s *Service) enqueue(tx *store.Tx, a Alert) (queued int, err error) {
	chs, err := tx.AlertChannels()
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return 0, fmt.Errorf("alerts: marshal alert: %w", err)
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
			return queued, err
		}
		queued++
	}
	return queued, nil
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
