package alerts_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/store"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// A fake network: each channel's address is a host, and what a host does with
// a request is the test's to say. No test here touches a real network.
type network struct {
	mu       sync.Mutex
	handlers map[string]func(*http.Request) (*http.Response, error)
	got      map[string][]received
}

type received struct {
	Header http.Header
	Body   string
	Path   string
	At     time.Time
}

func newNetwork() *network {
	return &network{handlers: map[string]func(*http.Request) (*http.Response, error){}, got: map[string][]received{}}
}

// on sets how host answers. Without a handler a host answers 200.
func (n *network) on(host string, h func(*http.Request) (*http.Response, error)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.handlers[host] = h
}

func (n *network) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	n.mu.Lock()
	n.got[r.URL.Host] = append(n.got[r.URL.Host], received{Header: r.Header.Clone(), Body: string(body), Path: r.URL.Path, At: time.Now()})
	h := n.handlers[r.URL.Host]
	n.mu.Unlock()
	if h == nil {
		return status(200), nil
	}
	return h(r)
}

// received is what host has been sent so far.
func (n *network) received(host string) []received {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]received(nil), n.got[host]...)
}

func status(code int) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}
}

// hang is a host that never answers until the request is cancelled.
func hang(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

// install is a store, a fake network and an alerts service over them.
type install struct {
	t     *testing.T
	store *store.Store
	net   *network
	svc   *alerts.Service
	// secrets are the plaintext configs the vault would hold, by channel id.
	secrets map[string][]byte
}

func newInstall(t *testing.T, mutate ...func(*alerts.Options)) *install {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Do(context.Background(), func(tx *store.Tx) error {
		return tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: t0})
	}); err != nil {
		t.Fatal(err)
	}
	in := &install{t: t, store: st, net: newNetwork(), secrets: map[string][]byte{}}
	o := alerts.Options{
		Store:  st,
		Now:    time.Now,
		Client: &http.Client{Transport: in.net},
		Secret: func(id string) ([]byte, bool) { b, ok := in.secrets[id]; return b, ok },
	}
	for _, m := range mutate {
		m(&o)
	}
	in.svc = alerts.New(o)
	return in
}

// channel adds a live channel whose address is https://host/hook.
func (in *install) channel(id, kind, host, minSeverity string, lifecycle bool, signing string) {
	in.t.Helper()
	cfg, _ := json.Marshal(store.AlertChannelConfig{URL: "https://" + host + "/hook/TOKEN-" + id, SigningSecret: signing})
	in.secrets[id] = cfg
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		_, err := tx.CreateAlertChannel(store.AlertChannel{
			ID: id, Name: id, Kind: kind, ConfigCiphertext: []byte("sealed"), Nonce: []byte("n"), DataKeyID: "dek_1",
			DisplayHost: host, MinSeverity: minSeverity, LifecycleAlerts: lifecycle, CreatedAt: t0, UpdatedAt: t0,
		})
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
}

// run starts the dispatcher and returns a function that stops it.
func (in *install) run() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { in.svc.Run(ctx) })
	return func() { cancel(); wg.Wait() }
}

// outbox is what the store holds for channel id.
func (in *install) outbox(id string) store.OutboxCount {
	in.t.Helper()
	var c store.OutboxCount
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		all, err := tx.OutboxStats()
		c = all[id]
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
	return c
}

func sample() alerts.Alert {
	return alerts.Alert{
		Kind: "audit", Severity: alerts.High, Title: "Key ci-bot was disabled",
		KeyID: "abcd1234", KeyName: "ci-bot", Time: t0,
		Evidence: []alerts.Fact{{Label: "Actor", Value: "owner"}, {Label: "Action", Value: "key.disable"}},
		Commands: []string{"kman key show abcd1234", "kman key enable abcd1234"},
	}
}
