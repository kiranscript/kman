package events

import (
	"bytes"
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// MetricType is how Prometheus treats a metric.
type MetricType string

const (
	Counter MetricType = "counter"
	Gauge   MetricType = "gauge"
)

// Sample is one value of a metric, under its labels.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Metric is a metric that is read when it is scraped. The caller supplies those
// that aren't counted from events, such as the age of the price table.
type Metric struct {
	Name, Help string
	Type       MetricType
	Samples    func() []Sample
}

// Metrics counts events for Prometheus. It is fed by a subscription, so counting
// is off the request path, and a scrape reads only what has been counted. A
// metric never carries content, a client address or anything an app chooses,
// such as a model or a path: labels are key ids, upstream ids, status classes
// and kman's own error codes, which are few.
type Metrics struct {
	mu       sync.Mutex
	requests map[[3]string]float64 // key, upstream, status class
	blocked  map[[2]string]float64 // key, code
	tokens   map[[2]string]float64 // key, type
	cost     map[string]float64    // key, microdollars
	unpriced map[string]float64    // key
}

// NewMetrics returns an empty Metrics.
func NewMetrics() *Metrics {
	return &Metrics{
		requests: map[[3]string]float64{}, blocked: map[[2]string]float64{}, tokens: map[[2]string]float64{},
		cost: map[string]float64{}, unpriced: map[string]float64{},
	}
}

// Run counts what sub delivers until it is closed.
func (m *Metrics) Run(sub *Subscription) {
	for e := range sub.C {
		m.count(e)
	}
}

func (m *Metrics) count(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.Kind == KindBlocked {
		m.blocked[[2]string{e.KeyID, e.BlockedCode}]++
		return
	}
	m.requests[[3]string{e.KeyID, e.UpstreamID, statusClass(e.Status)}]++
	for typ, n := range map[string]int64{
		"input": e.TokensIn, "output": e.TokensOut, "cached": e.TokensCached, "cache_write": e.TokensCacheWrite,
		"audio_input": e.TokensAudioIn, "audio_output": e.TokensAudioOut,
	} {
		if n > 0 {
			m.tokens[[2]string{e.KeyID, typ}] += float64(n)
		}
	}
	if e.CostKnown {
		m.cost[e.KeyID] += float64(e.CostMicro)
	} else {
		m.unpriced[e.KeyID]++
	}
}

func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "none"
	}
	return strconv.Itoa(status/100) + "xx"
}

// Handler serves the metrics in the Prometheus text format, with extra after
// them. It does no authentication: its caller decides who may scrape.
func (m *Metrics) Handler(extra ...Metric) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		for _, metric := range slices.Concat(m.counted(), extra) {
			writeMetric(&b, metric)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write(b.Bytes())
	})
}

// counted is the metrics counted from events, as a snapshot.
func (m *Metrics) counted() []Metric {
	m.mu.Lock()
	defer m.mu.Unlock()
	var requests, blocked, tokens, cost, unpriced []Sample
	for k, v := range m.requests {
		requests = append(requests, Sample{map[string]string{"key_id": k[0], "upstream_id": k[1], "status_class": k[2]}, v})
	}
	for k, v := range m.blocked {
		blocked = append(blocked, Sample{map[string]string{"key_id": k[0], "code": k[1]}, v})
	}
	for k, v := range m.tokens {
		tokens = append(tokens, Sample{map[string]string{"key_id": k[0], "type": k[1]}, v})
	}
	for k, v := range m.cost {
		cost = append(cost, Sample{map[string]string{"key_id": k}, v / 1e6})
	}
	for k, v := range m.unpriced {
		unpriced = append(unpriced, Sample{map[string]string{"key_id": k}, v})
	}
	fixed := func(s []Sample) func() []Sample { return func() []Sample { return s } }
	return []Metric{
		{"kman_requests_total", "Requests forwarded to an upstream, by key, upstream and the status the app was sent.", Counter, fixed(requests)},
		{"kman_blocked_requests_total", "Requests kman refused, by key and error code.", Counter, fixed(blocked)},
		{"kman_tokens_total", "Tokens reported by upstreams, by key and kind.", Counter, fixed(tokens)},
		{"kman_cost_usd_total", "Cost of forwarded requests in US dollars, by key. Estimates are included.", Counter, fixed(cost)},
		{"kman_unpriced_requests_total", "Forwarded requests whose cost is unknown, by key.", Counter, fixed(unpriced)},
	}
}

// BusMetrics are the bus's own: what it dropped and what waits in it.
func BusMetrics(b *Bus) []Metric {
	return []Metric{
		{"kman_events_dropped_total", "Events dropped because a queue lane was full.", Counter, func() []Sample {
			st := b.Stats()
			return []Sample{
				{map[string]string{"lane": "ordinary"}, float64(st.Dropped)},
				{map[string]string{"lane": "priority"}, float64(st.DroppedPriority)},
			}
		}},
		{"kman_event_queue_depth", "Events waiting to be fanned out.", Gauge, func() []Sample {
			return []Sample{{Value: float64(b.Stats().Queued)}}
		}},
		{"kman_event_subscriber_dropped_total", "Events a subscriber did not take because it was behind.", Counter, func() []Sample {
			var out []Sample
			for _, s := range b.Subscriptions() {
				out = append(out, Sample{map[string]string{"subscriber": s.Name}, float64(s.Dropped())})
			}
			return out
		}},
	}
}

// WriterMetrics are the store writer's: what it saved and what it could not.
func WriterMetrics(w *Writer) []Metric {
	one := func(name, help string, get func(WriterStats) int64) Metric {
		return Metric{name, help, Counter, func() []Sample { return []Sample{{Value: float64(get(w.Stats()))}} }}
	}
	return []Metric{
		one("kman_events_written_total", "Events saved to the store.", func(s WriterStats) int64 { return s.Written }),
		one("kman_event_write_failures_total", "Writes to the store that failed and will be retried.", func(s WriterStats) int64 { return s.Failures }),
		one("kman_events_lost_total", "Events given up because the store could not be written to.", func(s WriterStats) int64 { return s.Lost }),
	}
}

func writeMetric(b *bytes.Buffer, m Metric) {
	b.WriteString("# HELP " + m.Name + " " + m.Help + "\n# TYPE " + m.Name + " " + string(m.Type) + "\n")
	samples := m.Samples()
	slices.SortFunc(samples, func(x, y Sample) int { return cmp.Compare(labelString(x.Labels), labelString(y.Labels)) })
	for _, s := range samples {
		b.WriteString(m.Name + labelString(s.Labels) + " " + strconv.FormatFloat(s.Value, 'g', -1, 64) + "\n")
	}
}

// labelString renders labels as {a="1",b="2"}, sorted by name, or nothing.
func labelString(l map[string]string) string {
	if len(l) == 0 {
		return ""
	}
	names := make([]string, 0, len(l))
	for n := range l {
		names = append(names, n)
	}
	slices.Sort(names)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(n + `="` + labelEscaper.Replace(l[n]) + `"`)
	}
	sb.WriteByte('}')
	return sb.String()
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
