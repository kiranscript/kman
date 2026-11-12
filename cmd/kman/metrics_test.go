package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

func TestMetricsListenIsOptInAndMustBeHostAndPort(t *testing.T) {
	c, err := loadConfig(writeConfig(t, goodConfig), noEnv)
	if err != nil || c.MetricsListen != "" {
		t.Fatalf("with no metrics_listen: %v, %q; want none, which is the default", err, c.MetricsListen)
	}
	c, err = loadConfig(writeConfig(t, goodConfig+"metrics_listen: 127.0.0.1:9090\n"), noEnv)
	if err != nil || c.MetricsListen != "127.0.0.1:9090" {
		t.Fatalf("with metrics_listen: %v, %q", err, c.MetricsListen)
	}
	for name, v := range map[string]string{"no port": "127.0.0.1", "a socket": "unix:/run/kman/metrics.sock"} {
		_, err := loadConfig(writeConfig(t, goodConfig+"metrics_listen: "+v+"\n"), noEnv)
		if err == nil || !strings.Contains(err.Error(), "metrics_listen") {
			t.Errorf("%s: loadConfig = %v, want an error naming metrics_listen", name, err)
		}
	}
	// A unix admin_listen returns early from the checks, but metrics_listen is still checked.
	yaml := strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: "+adminSock, 1) + "metrics_listen: nonsense\n"
	if _, err := loadConfig(writeConfig(t, yaml), noEnv); err == nil || !strings.Contains(err.Error(), "metrics_listen") {
		t.Errorf("a bad metrics_listen beside a socket admin_listen: %v", err)
	}
}

// fakeAuth accepts one secret.
type fakeAuth struct{ secret string }

func (f fakeAuth) Authenticate(_ context.Context, secret string) (string, error) {
	switch secret {
	case f.secret:
		return "owner", nil
	case "broken":
		return "", errors.New("the store is locked")
	}
	return "", &admin.Error{Code: admin.CodeUnauthorized, Message: "no"}
}

func TestMetricsOnTheAdminListenerNeedAnAdminToken(t *testing.T) {
	const body = "kman_metric 1\n"
	h := requireAdminToken(fakeAuth{"kma_good"}, slog.New(slog.DiscardHandler), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	for _, tc := range []struct {
		name, auth string
		want       int
	}{
		{"no token", "", 401},
		{"a bad token", "Bearer kma_bad", 401},
		{"a token that isn't a bearer", "kma_good", 401},
		{"a store failure", "Bearer broken", 500},
		{"the admin token", "Bearer kma_good", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d", rec.Code, tc.want)
			}
			if got := strings.Contains(rec.Body.String(), "kman_metric"); got != (tc.want == 200) {
				t.Errorf("body %q: metrics shown = %v", rec.Body.String(), got)
			}
			if tc.want == 401 && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("a 401 without WWW-Authenticate")
			}
		})
	}
}

func TestThePriceDataAgeIsReportedInSeconds(t *testing.T) {
	generated := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := priceAgeMetric(func() time.Time { return generated.Add(36 * time.Hour) }, generated)
	if m.Name != "kman_price_data_age_seconds" {
		t.Fatalf("name %q", m.Name)
	}
	if got := m.Samples(); len(got) != 1 || got[0].Value != 36*3600 {
		t.Errorf("samples %+v, want 129600", got)
	}
}
