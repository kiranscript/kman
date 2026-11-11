# Events and metrics

Every request kman handles produces one **event**: written to the store, counted per key per hour, and counted for Prometheus. An event says what happened and never what was said: no request or response content, no header, no query string, no secret.

## What an event holds

The time, the key id (none when the request presented no key kman could identify), the client address, the upstream, the model (only when it is a plain model id), the method and path with no query, the status the app was sent, the error code when kman refused the request, latency, request and response bytes, tokens by kind, the cost in microdollars (or *unknown*), and whether the cost is an estimate. Country and ASN stay empty until a GeoIP database is supported.

- A request kman forwarded is kind `request`. That includes one kman answered with `502 upstream_unavailable` because the upstream could not be reached.
- A request kman refused before it reached an upstream is kind `blocked`, with its error code.
- A **disabled key**, and a **canary** (a key with no credential), take a priority lane inside kman, so that a later alert never waits behind ordinary traffic.

Events are written to the store in batches every 5 seconds, and when kman stops. They are kept 30 days by default once retention ships; v0.1.0 does not prune them yet.

If the store cannot be written to, kman keeps the unsaved events in memory (up to 10,000) and retries. Past that it gives up the oldest and counts them in `kman_events_lost_total`.

## Hourly usage

`usage_hourly` holds one row per key per hour, increased with each batch: forwarded requests, refused requests, upstream 4xx and 5xx answers, tokens in and out, cost, and the requests whose cost is unknown. It outlives the events: 400 days by default.

## Prometheus metrics

| Metric | Labels | Meaning |
|---|---|---|
| `kman_requests_total` | `key_id`, `upstream_id`, `status_class` | Forwarded requests, by the status the app was sent (`2xx`, `4xx`, `5xx`, `none`) |
| `kman_blocked_requests_total` | `key_id`, `code` | Requests kman refused, by error code. `key_id` is empty for a bad key |
| `kman_tokens_total` | `key_id`, `type` | Tokens: `input`, `output`, `cached`, `cache_write`, `audio_input`, `audio_output` |
| `kman_cost_usd_total` | `key_id` | Cost in dollars, estimates included |
| `kman_unpriced_requests_total` | `key_id` | Forwarded requests whose cost is unknown |
| `kman_events_dropped_total` | `lane` | Events dropped because the queue was full (`ordinary`, `priority`) |
| `kman_event_queue_depth` | | Events waiting to be handed on |
| `kman_event_subscriber_dropped_total` | `subscriber` | Events a subscriber was too far behind to take |
| `kman_events_written_total`, `kman_event_write_failures_total`, `kman_events_lost_total` | | The store writer |
| `kman_price_data_age_seconds` | | Seconds since the embedded price table was generated |

Counters count from when kman starts, as Prometheus expects. The events in the store are the durable record. No label holds a model, a path, or a client address, which an app chooses or which identifies a caller.

**A full queue never slows a request.** kman drops the event instead and counts it: watch `kman_events_dropped_total`.

### Where to scrape

The admin listener serves `/metrics` and asks for an admin token like the rest of the admin API:

```
curl -H "Authorization: Bearer $KMAN_ADMIN_TOKEN" http://127.0.0.1:8081/metrics
```

For a scraper that cannot send a token, set `metrics_listen` in `kman.yaml`. It is **off by default**:

```yaml
metrics_listen: 127.0.0.1:9090
```

That listener serves `/metrics` and nothing else, **with no authentication**, over plain HTTP. Keep it on loopback or a private network; kman logs a warning if it is not on loopback. The metrics hold key ids and spend, which tell a reader which keys are busy. Restart kman after editing `kman.yaml`.
