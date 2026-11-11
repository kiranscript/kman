package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// A local alert receiver: the address of a webhook channel in these tests.
type alertSink struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []string
}

func newAlertSink(t *testing.T) *alertSink {
	t.Helper()
	k := &alertSink{}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		k.mu.Lock()
		k.got = append(k.got, string(b))
		k.mu.Unlock()
	}))
	t.Cleanup(k.srv.Close)
	return k
}

func (k *alertSink) bodies() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.got...)
}

func (k *alertSink) waitFor(t *testing.T, parts ...string) string {
	t.Helper()
	for range 400 {
		for _, b := range k.bodies() {
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(b, p)
			}
			if ok {
				return b
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no alert with %q arrived; got %v", parts, k.bodies())
	return ""
}

// seedBudgetInstall makes (on the openai upstream newRekeyHome seeds) a key with a policy and an expiry, a
// webhook channel at sink, and what the key has spent today, which is
// 2026-11-04 on testSys's clock.
func seedBudgetInstall(t *testing.T, sink *alertSink, policyJSON string, spentToday int64, ttl time.Duration) func(rekeyHome) {
	return func(h rekeyHome) {
		var keyID string
		h.withService(t, vault.FileSource(h.keyFile()), func(ctx context.Context, svc *admin.Service) {
			ttlSeconds := int64(ttl / time.Second)
			created, err := svc.CreateKey(ctx, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai", TTLSeconds: &ttlSeconds,
				Policy: []byte(policyJSON)})
			if err != nil {
				t.Fatal(err)
			}
			keyID = created.Key.ID
			if _, err := svc.AddChannel(ctx, admin.AddChannelRequest{Name: "ops", Kind: "webhook", URL: admin.Secret(sink.srv.URL + "/in/TOKEN"), MinSeverity: "low"}); err != nil {
				t.Fatal(err)
			}
		})
		// The key was made 30 days ago, so a week's notice of its expiry is due.
		h.sqlExec(t, "UPDATE virtual_keys SET created_at = created_at - 30 * 86400000")
		st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.Do(t.Context(), func(tx *store.Tx) error {
			return tx.AddSpend([]store.SpendRow{{KeyID: keyID, Day: "2026-11-04", CostMicro: spentToday, Requests: 3}})
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// kman serve checks spend and expiry as it starts, and tells the channel the
// operator added: this is the composition, with the real store, the real policy
// reader and the real settings.
func TestServeWarnsOfASpendThresholdAndAnExpiryNearingAtStart(t *testing.T) {
	sink := newAlertSink(t)
	s, _, _ := testSys(t, nil)
	serveHome(t, s, "", seedBudgetInstall(t, sink, `{"daily_cap_micro":10000000}`, 8_500_000, 3*24*time.Hour))
	got := sink.waitFor(t, "80%", "daily cap", "my-app", "$8.50")
	if strings.Contains(got, "TOKEN") {
		t.Errorf("an alert holds the channel's address:\n%s", got)
	}
	sink.waitFor(t, "expires within 7 days", "my-app")
}

// A key's own percentages and the install's setting reach the warnings through
// the real policy and settings.
func TestServeUsesAKeysOwnPercentsAndASoftBudgetWithNoCap(t *testing.T) {
	sink := newAlertSink(t)
	s, _, _ := testSys(t, nil)
	serveHome(t, s, "", seedBudgetInstall(t, sink, `{"daily_budget_micro":10000000,"soft_budget_percents":[10]}`, 1_200_000, 80*24*time.Hour))
	sink.waitFor(t, "10%", "daily soft budget", "my-app")
}
