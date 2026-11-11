package store

import (
	"database/sql"
	"time"
)

// EventRow is one event, from the events table (SPEC §9). It holds no request or
// response content. A text field that is empty and a number that is zero are
// stored as no value, except the cost: CostKnown says whether CostMicro, which
// can be a known zero, means anything.
type EventRow struct {
	Time time.Time
	Kind string // request, blocked or session
	// KeyID is empty when the request presented no key kman could identify.
	KeyID    string
	ClientIP string
	Country  string
	ASN      int64

	UpstreamID, Model, Operation string

	Status      int
	BlockedCode string
	LatencyMS   int64
	ReqBytes    int64
	RespBytes   int64

	TokensIn, TokensOut, TokensCached, TokensCacheWrite, TokensAudioIn, TokensAudioOut int64

	CostMicro     int64
	CostKnown     bool
	CostEstimated bool
}

// UsageRow is one key's usage for one hour, from the usage_hourly table. When
// rows are added, the amounts are increments. Money is microdollars.
type UsageRow struct {
	KeyID string
	Hour  time.Time // the start of the hour, UTC

	Requests  int64 // forwarded requests and realtime sessions
	Blocked   int64 // requests kman refused
	Errors4xx int64 // forwarded requests the upstream answered with a 4xx
	Errors5xx int64 // ... or a 5xx, which includes kman's own 502 for an upstream it could not reach
	TokensIn  int64
	TokensOut int64
	CostMicro int64
	Unmetered int64 // forwarded requests whose cost is unknown
}

const hourMS = int64(time.Hour / time.Millisecond)

// AddEvents stores the events and adds what they used to usage_hourly, in this
// transaction, so a batch is kept whole or not at all. An event with no key is
// stored but counts toward no key's usage.
func (tx *Tx) AddEvents(rows []EventRow) error {
	insert, err := tx.tx.PrepareContext(tx.ctx, `INSERT INTO events (ts, key_id, kind, client_ip, country, asn, upstream_id, model, operation,
		status, blocked_code, latency_ms, req_bytes, resp_bytes, tokens_in, tokens_out, tokens_cached, tokens_cache_write,
		tokens_audio_in, tokens_audio_out, cost_micro, cost_estimated)
		VALUES (?1, NULLIF(?2, ''), ?3, NULLIF(?4, ''), NULLIF(?5, ''), NULLIF(?6, 0), NULLIF(?7, ''), NULLIF(?8, ''), NULLIF(?9, ''),
		NULLIF(?10, 0), NULLIF(?11, ''), ?12, ?13, ?14, ?15, ?16, ?17, ?18, ?19, ?20, ?21, ?22)`)
	if err != nil {
		return typed(err, "add events")
	}
	defer insert.Close()
	usage := map[usageKey]*UsageRow{}
	var order []usageKey
	for _, r := range rows {
		var cost any // NULL when the cost is unknown
		if r.CostKnown {
			cost = r.CostMicro
		}
		if _, err := insert.ExecContext(tx.ctx, toMilli(r.Time), r.KeyID, r.Kind, r.ClientIP, r.Country, r.ASN, r.UpstreamID, r.Model, r.Operation,
			r.Status, r.BlockedCode, r.LatencyMS, r.ReqBytes, r.RespBytes, r.TokensIn, r.TokensOut, r.TokensCached, r.TokensCacheWrite,
			r.TokensAudioIn, r.TokensAudioOut, cost, boolInt(r.CostEstimated)); err != nil {
			return typed(err, "add events")
		}
		if r.KeyID == "" {
			continue
		}
		k := usageKey{r.KeyID, toMilli(r.Time) / hourMS * hourMS}
		u := usage[k]
		if u == nil {
			u = &UsageRow{}
			usage[k] = u
			order = append(order, k)
		}
		if r.Kind == "blocked" {
			u.Blocked++
			continue
		}
		u.Requests++
		switch {
		case r.Status >= 400 && r.Status < 500:
			u.Errors4xx++
		case r.Status >= 500:
			u.Errors5xx++
		}
		u.TokensIn += r.TokensIn
		u.TokensOut += r.TokensOut
		if r.CostKnown {
			u.CostMicro += r.CostMicro
		} else {
			u.Unmetered++
		}
	}
	for _, k := range order {
		u := usage[k]
		if _, err := tx.tx.ExecContext(tx.ctx, `INSERT INTO usage_hourly (key_id, hour, requests, blocked, errors_4xx, errors_5xx, tokens_in, tokens_out, cost_micro, unmetered)
			VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)
			ON CONFLICT (key_id, hour) DO UPDATE SET
				requests = requests + excluded.requests,
				blocked = blocked + excluded.blocked,
				errors_4xx = errors_4xx + excluded.errors_4xx,
				errors_5xx = errors_5xx + excluded.errors_5xx,
				tokens_in = tokens_in + excluded.tokens_in,
				tokens_out = tokens_out + excluded.tokens_out,
				cost_micro = cost_micro + excluded.cost_micro,
				unmetered = unmetered + excluded.unmetered`,
			k.key, k.hour, u.Requests, u.Blocked, u.Errors4xx, u.Errors5xx, u.TokensIn, u.TokensOut, u.CostMicro, u.Unmetered); err != nil {
			return typed(err, "add usage of key "+k.key)
		}
	}
	return nil
}

type usageKey struct {
	key  string
	hour int64
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// EventsSince returns the events at or after since, oldest first.
func (tx *Tx) EventsSince(since time.Time) ([]EventRow, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT ts, COALESCE(key_id, ''), kind, COALESCE(client_ip, ''), COALESCE(country, ''),
		COALESCE(asn, 0), COALESCE(upstream_id, ''), COALESCE(model, ''), COALESCE(operation, ''), COALESCE(status, 0),
		COALESCE(blocked_code, ''), COALESCE(latency_ms, 0), COALESCE(req_bytes, 0), COALESCE(resp_bytes, 0),
		COALESCE(tokens_in, 0), COALESCE(tokens_out, 0), COALESCE(tokens_cached, 0), COALESCE(tokens_cache_write, 0),
		COALESCE(tokens_audio_in, 0), COALESCE(tokens_audio_out, 0), cost_micro, cost_estimated
		FROM events WHERE ts >= ? ORDER BY ts, id`, toMilli(since))
	if err != nil {
		return nil, typed(err, "read events")
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var r EventRow
		var ts int64
		var cost sql.NullInt64
		var estimated int
		if err := rows.Scan(&ts, &r.KeyID, &r.Kind, &r.ClientIP, &r.Country, &r.ASN, &r.UpstreamID, &r.Model, &r.Operation, &r.Status,
			&r.BlockedCode, &r.LatencyMS, &r.ReqBytes, &r.RespBytes, &r.TokensIn, &r.TokensOut, &r.TokensCached, &r.TokensCacheWrite,
			&r.TokensAudioIn, &r.TokensAudioOut, &cost, &estimated); err != nil {
			return nil, typed(err, "read events")
		}
		r.Time = fromMilli(ts)
		r.CostMicro, r.CostKnown, r.CostEstimated = cost.Int64, cost.Valid, estimated == 1
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "read events")
	}
	return out, nil
}

// UsageSince returns every hour at or after the hour containing since, by key
// and hour.
func (tx *Tx) UsageSince(since time.Time) ([]UsageRow, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT key_id, hour, requests, blocked, errors_4xx, errors_5xx, tokens_in, tokens_out, cost_micro, unmetered
		FROM usage_hourly WHERE hour >= ? ORDER BY key_id, hour`, toMilli(since)/hourMS*hourMS)
	if err != nil {
		return nil, typed(err, "read usage")
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		var hour int64
		if err := rows.Scan(&r.KeyID, &hour, &r.Requests, &r.Blocked, &r.Errors4xx, &r.Errors5xx, &r.TokensIn, &r.TokensOut, &r.CostMicro, &r.Unmetered); err != nil {
			return nil, typed(err, "read usage")
		}
		r.Hour = fromMilli(hour)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "read usage")
	}
	return out, nil
}
