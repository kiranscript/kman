package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// Alerter is the alert service, as admin uses it. The composition root passes
// the alerts module, which admin may not import (SPEC §2).
type Alerter interface {
	// AlertAudit queues the alerts for an audit entry in the transaction that
	// appended it, so a change and its alert commit together.
	AlertAudit(tx *store.Tx, e store.AuditEntry) error
	// Wake tells the dispatcher to look at its outbox: it is called after a
	// command has committed.
	Wake()
	// Test sends one test alert to a channel at once and says why it failed.
	Test(ctx context.Context, channelID string) error
}

// The kinds of alert channel (SPEC §9; Discord and ntfy join in M2).
const (
	channelSlack   = "slack"
	channelWebhook = "webhook"
)

var severities = []string{"low", "medium", "high", "critical"}

// maxChannelURL is the longest channel address accepted.
const maxChannelURL = 2048

// Channel is an alert channel as the admin API shows it. It never holds the
// address or the signing secret: a Slack address is itself a secret.
type Channel struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind" doc:"slack or webhook."`
	Host            string    `json:"host" doc:"The host of its address, which is all of the address that is shown."`
	MinSeverity     string    `json:"min_severity" doc:"The lowest severity it takes: low, medium, high or critical."`
	LifecycleAlerts bool      `json:"lifecycle_alerts" doc:"Whether it also takes spend and lifecycle warnings."`
	CreatedAt       time.Time `json:"created_at"`
	Pending         int       `json:"pending,omitempty" doc:"How many alerts wait in the outbox for it."`
	Failed          int       `json:"failed,omitempty" doc:"How many alerts were given up on."`
	Delivered       int       `json:"delivered,omitempty" doc:"How many alerts it has been sent, until they are pruned."`
	LastError       string    `json:"last_error,omitempty" doc:"Why the latest failed attempt failed. Never the address."`
}

// ChannelList is every channel that is not removed, by name.
type ChannelList struct {
	Channels []Channel `json:"channels"`
}

// ChannelRef names a channel.
type ChannelRef struct {
	Ref string `json:"-" path:"ref" doc:"The channel's name or id."`
}

// AddChannelRequest is a new alert channel.
type AddChannelRequest struct {
	Name            string `json:"name" doc:"1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among channels not removed."`
	Kind            string `json:"kind" doc:"slack or webhook."`
	URL             Secret `json:"url" doc:"Where alerts go: a Slack incoming-webhook address, or the webhook's. It is https, or http to this machine. kman seals it and never shows it again."`
	MinSeverity     string `json:"min_severity,omitempty" doc:"The lowest severity the channel takes: low, medium, high or critical. Default medium."`
	LifecycleAlerts *bool  `json:"lifecycle_alerts,omitempty" doc:"Whether the channel also takes spend and lifecycle warnings. Default true."`
}

// CreatedChannel is a new channel. A webhook's signing secret is shown only here.
type CreatedChannel struct {
	Channel       Channel `json:"channel"`
	SigningSecret Secret  `json:"signing_secret,omitempty" doc:"For a webhook, the secret that signs each request (kmwhsec_...), shown only this once. A receiver recomputes the signature with it."`
}

// ChannelTestResult says how a test alert fared. A channel that does not answer
// is a result, not an error.
type ChannelTestResult struct {
	Channel string `json:"channel" doc:"The channel's name."`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty" doc:"Why the test alert did not arrive. Never the address."`
}

func channelDTO(c store.AlertChannel, n store.OutboxCount) Channel {
	return Channel{ID: c.ID, Name: c.Name, Kind: c.Kind, Host: c.DisplayHost, MinSeverity: c.MinSeverity,
		LifecycleAlerts: c.LifecycleAlerts, CreatedAt: c.CreatedAt,
		Pending: n.Pending, Failed: n.Failed, Delivered: n.Delivered, LastError: n.LastError}
}

// checkChannelURL reports what is wrong with a channel's address, and returns
// its host. The message never quotes the address.
func checkChannelURL(raw string) (host string, err error) {
	if raw == "" {
		return "", errorf(CodeInvalid, "no address given")
	}
	if len(raw) > maxChannelURL {
		return "", errorf(CodeInvalid, "the address is over %d bytes", maxChannelURL)
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "" || u.Hostname() == "":
		return "", errorf(CodeInvalid, "the address is not a URL with a scheme and a host, such as https://hooks.example.com/...")
	case u.User != nil:
		return "", errorf(CodeInvalid, "the address holds a login: put what authenticates in the path or a query, which Slack and most webhooks do")
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
	case u.Scheme == "http":
		return "", errorf(CodeInvalid, "plain http is allowed only to this machine: use https")
	default:
		return "", errorf(CodeInvalid, "the address must be https")
	}
	return u.Host, nil
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// newSigningSecret returns kmwhsec_ and 32 random bytes in hex.
func newSigningSecret(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var b [32]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", errors.Join(errors.New("admin: random signing secret"), err)
	}
	return "kmwhsec_" + hex.EncodeToString(b[:]), nil
}

// AddChannel seals a channel's address and adds the channel. A webhook gets a
// signing secret, returned only here.
func (s *Service) AddChannel(ctx context.Context, req AddChannelRequest) (CreatedChannel, error) {
	if err := checkName("channel", req.Name); err != nil {
		return CreatedChannel{}, err
	}
	if req.Kind != channelSlack && req.Kind != channelWebhook {
		return CreatedChannel{}, errorf(CodeInvalid, "channel kind %q: use slack or webhook", req.Kind)
	}
	minSeverity := req.MinSeverity
	if minSeverity == "" {
		minSeverity = "medium"
	}
	if !slices.Contains(severities, minSeverity) {
		return CreatedChannel{}, errorf(CodeInvalid, "min_severity %q: use %s", minSeverity, strings.Join(severities, ", "))
	}
	host, err := checkChannelURL(string(req.URL))
	if err != nil {
		return CreatedChannel{}, err
	}
	if s.vault == nil {
		return CreatedChannel{}, errors.New("admin: no vault")
	}
	id, err := store.NewID(s.rand, "ch_")
	if err != nil {
		return CreatedChannel{}, err
	}
	cfg := store.AlertChannelConfig{URL: string(req.URL)}
	if req.Kind == channelWebhook {
		if cfg.SigningSecret, err = newSigningSecret(s.rand); err != nil {
			return CreatedChannel{}, err
		}
	}
	plain, err := json.Marshal(cfg)
	if err != nil {
		return CreatedChannel{}, errors.Join(errors.New("admin: marshal channel config"), err)
	}
	defer clear(plain)
	sealed, err := s.vault.Seal(id, plain)
	if err != nil {
		return CreatedChannel{}, err
	}
	lifecycle := req.LifecycleAlerts == nil || *req.LifecycleAlerts
	var out CreatedChannel
	fx := &effects{}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		c, err := tx.CreateAlertChannel(store.AlertChannel{ID: id, Name: req.Name, Kind: req.Kind,
			ConfigCiphertext: sealed.Ciphertext, Nonce: sealed.Nonce, DataKeyID: sealed.DataKeyID, DisplayHost: host,
			MinSeverity: minSeverity, LifecycleAlerts: lifecycle, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return nil, fromStore(err, "channel "+req.Name)
		}
		fx.seal = append(fx.seal, sealedSecret{id, sealed})
		out.Channel = channelDTO(c, store.OutboxCount{})
		out.SigningSecret = Secret(cfg.SigningSecret)
		return []audit{{action: "alert_channel.create", kind: "alert_channel", id: id, after: out.Channel}}, nil
	})
	if err != nil {
		return CreatedChannel{}, err
	}
	return out, nil
}

// ListChannels returns every channel with what its outbox holds.
func (s *Service) ListChannels(ctx context.Context) (ChannelList, error) {
	out := ChannelList{Channels: []Channel{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		chs, err := tx.AlertChannels()
		if err != nil {
			return err
		}
		stats, err := tx.OutboxStats()
		if err != nil {
			return err
		}
		for _, c := range chs {
			out.Channels = append(out.Channels, channelDTO(c, stats[c.ID]))
		}
		return nil
	})
	return out, err
}

// RemoveChannel removes a channel, wipes its address, and gives up the alerts
// still waiting for it.
func (s *Service) RemoveChannel(ctx context.Context, req ChannelRef) (Channel, error) {
	var out Channel
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		c, err := channelByRef(tx, req.Ref)
		if err != nil {
			return nil, err
		}
		if err := tx.DeleteAlertChannel(c.ID, now); err != nil {
			return nil, fromStore(err, "channel "+req.Ref)
		}
		fx.wipe = append(fx.wipe, c.ID)
		out = channelDTO(c, store.OutboxCount{})
		return []audit{{action: "alert_channel.delete", kind: "alert_channel", id: c.ID, before: out}}, nil
	})
	return out, err
}

// TestChannel sends a test alert to a channel at once. It changes nothing, so
// it is not audited.
func (s *Service) TestChannel(ctx context.Context, req ChannelRef) (ChannelTestResult, error) {
	var c store.AlertChannel
	if err := s.read(ctx, func(tx *store.Tx) (err error) { c, err = channelByRef(tx, req.Ref); return }); err != nil {
		return ChannelTestResult{}, err
	}
	if s.alerts == nil {
		return ChannelTestResult{}, errors.New("admin: no alert service")
	}
	out := ChannelTestResult{Channel: c.Name, OK: true}
	if err := s.alerts.Test(ctx, c.ID); err != nil {
		out.OK, out.Error = false, err.Error()
	}
	return out, nil
}

// channelByRef finds a channel by id or by name.
func channelByRef(tx *store.Tx, ref string) (store.AlertChannel, error) {
	c, err := tx.AlertChannel(ref)
	if err == nil {
		return c, nil
	}
	if !isNotFound(err) {
		return store.AlertChannel{}, err
	}
	c, err = tx.AlertChannelByName(ref)
	if err != nil {
		return store.AlertChannel{}, fromStore(err, "channel "+ref)
	}
	return c, nil
}

func sealedOfChannel(c store.AlertChannel) vault.Sealed {
	return vault.Sealed{DataKeyID: c.DataKeyID, Nonce: c.Nonce, Ciphertext: c.ConfigCiphertext}
}
