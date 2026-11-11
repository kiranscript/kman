package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/store"
)

// eventPipeline is the event bus and what listens to it: the store writer
// (events and usage_hourly) and the Prometheus metrics. detect and the alert
// outbox join as more subscribers.
type eventPipeline struct {
	bus     *events.Bus
	metrics *events.Metrics
	writer  *events.Writer
	cancel  context.CancelFunc
	done    sync.WaitGroup
}

// startEvents runs the bus and its subscribers until stop.
func startEvents(ctx context.Context, st *store.Store, log *slog.Logger) *eventPipeline {
	p := &eventPipeline{
		bus:     events.New(events.Options{}),
		metrics: events.NewMetrics(),
		writer:  events.NewWriter(events.WriterOptions{Sink: events.SQLite(st), Log: log}),
	}
	// A writer behind by a whole queue's worth means the store is slow; the bus
	// counts what it then drops, and metrics show it.
	storeSub := p.bus.Subscribe("store", events.SubOptions{Buffer: 4 * events.DefaultCapacity})
	metricsSub := p.bus.Subscribe("metrics", events.SubOptions{Buffer: events.DefaultCapacity})
	ctx, p.cancel = context.WithCancel(context.WithoutCancel(ctx))
	p.done.Go(func() { p.bus.Run(ctx) })
	p.done.Go(func() { p.writer.Run(storeSub) })
	p.done.Go(func() { p.metrics.Run(metricsSub) })
	return p
}

// stop hands on what is queued, writes it, and ends. Call it once requests have
// stopped, and before the store closes.
func (p *eventPipeline) stop() {
	p.cancel()
	p.done.Wait()
}

// handler serves /metrics: what was counted, the bus's and the writer's own, the
// age of the price data, and any metric more adds.
func (p *eventPipeline) handler(now func() time.Time, priceDataGenerated time.Time, more ...events.Metric) http.Handler {
	extra := append(events.BusMetrics(p.bus), events.WriterMetrics(p.writer)...)
	extra = append(extra, priceAgeMetric(now, priceDataGenerated))
	extra = append(extra, more...)
	return p.metrics.Handler(extra...)
}

// priceAgeMetric is kman_price_data_age_seconds: how old the embedded price table
// is, which an operator alerts on when a release is overdue (ADR 0007).
func priceAgeMetric(now func() time.Time, generated time.Time) events.Metric {
	return events.Metric{
		Name: "kman_price_data_age_seconds", Type: events.Gauge,
		Help: "Seconds since the embedded price table was generated.",
		Samples: func() []events.Sample {
			return []events.Sample{{Value: now().Sub(generated).Seconds()}}
		},
	}
}

// priceDataGenerated is when the embedded price table was generated.
func priceDataGenerated() time.Time { return pricing.Builtin().GeneratedAt() }

// authenticator is what requireAdminToken asks about a token: admin's Authenticate.
type authenticator interface {
	Authenticate(ctx context.Context, secret string) (actor string, err error)
}

// requireAdminToken serves next to a request with a live admin token as its bearer
// token, as the admin API does, and nothing else: no cookie, no query parameter.
func requireAdminToken(auth authenticator, log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok {
			_, err := auth.Authenticate(r.Context(), strings.TrimSpace(secret))
			if err == nil {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
				return
			}
			if _, isAdmin := errors.AsType[*admin.Error](err); !isAdmin {
				log.Error("metrics: could not check an admin token", "err", err)
				http.Error(w, "internal error: see the kman serve log", http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="kman admin"`)
		http.Error(w, "send an admin token as Authorization: Bearer kma_...", http.StatusUnauthorized)
	})
}

// metricsOnly serves h at /metrics and nothing else, for the opt-in metrics
// listener, which has no authentication.
func metricsOnly(h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", h)
	return mux
}
