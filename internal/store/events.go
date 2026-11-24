package store

import (
	"database/sql"
	"strings"
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
	// Client is the User-Agent reduced to a product and major version, or none
	// when the request sent none (events.ClientOf). Empty for an event stored
	// before kman recorded it. It is recorded for a request with no key too.
	Client string
	// ASOrg is the organisation the ASN belongs to. It is not kept in the events
	// table: it labels the key's place for the ASN, so it reads back empty from
	// EventsSince and is the place's label from ListEvents.
	ASOrg string

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

// AddEvents stores the events, adds what they used to usage_hourly, and adds
// where they came from to their keys' places, in this transaction, so a batch is
// kept whole or not at all. An event with no key is stored but counts toward no
// key's usage or places.
func (tx *Tx) AddEvents(rows []EventRow) error {
	insert, err := tx.tx.PrepareContext(tx.ctx, `INSERT INTO events (ts, key_id, kind, client_ip, country, asn, upstream_id, model, operation,
		status, blocked_code, latency_ms, req_bytes, resp_bytes, tokens_in, tokens_out, tokens_cached, tokens_cache_write,
		tokens_audio_in, tokens_audio_out, cost_micro, cost_estimated, client)
		VALUES (?1, NULLIF(?2, ''), ?3, NULLIF(?4, ''), NULLIF(?5, ''), NULLIF(?6, 0), NULLIF(?7, ''), NULLIF(?8, ''), NULLIF(?9, ''),
		NULLIF(?10, 0), NULLIF(?11, ''), ?12, ?13, ?14, ?15, ?16, ?17, ?18, ?19, ?20, ?21, ?22, NULLIF(?23, ''))`)
	if err != nil {
		return typed(err, "add events")
	}
	defer insert.Close()
	usage := map[usageKey]*UsageRow{}
	var order []usageKey
	places := newPlaceTally()
	for _, r := range rows {
		places.add(r)
		var cost any // NULL when the cost is unknown
		if r.CostKnown {
			cost = r.CostMicro
		}
		if _, err := insert.ExecContext(tx.ctx, toMilli(r.Time), r.KeyID, r.Kind, r.ClientIP, r.Country, r.ASN, r.UpstreamID, r.Model, r.Operation,
			r.Status, r.BlockedCode, r.LatencyMS, r.ReqBytes, r.RespBytes, r.TokensIn, r.TokensOut, r.TokensCached, r.TokensCacheWrite,
			r.TokensAudioIn, r.TokensAudioOut, cost, boolInt(r.CostEstimated), r.Client); err != nil {
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
	return places.write(tx.ctx, tx.tx)
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
		COALESCE(tokens_audio_in, 0), COALESCE(tokens_audio_out, 0), cost_micro, cost_estimated, COALESCE(client, '')
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
			&r.TokensAudioIn, &r.TokensAudioOut, &cost, &estimated, &r.Client); err != nil {
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

// FirstUsageHour is the start of the earliest hour usage_hourly holds a row for,
// of any key, and the zero time when it holds none. It is how far back kman has
// been counting.
func (tx *Tx) FirstUsageHour() (time.Time, error) {
	var hour sql.NullInt64
	if err := tx.tx.QueryRowContext(tx.ctx, `SELECT MIN(hour) FROM usage_hourly`).Scan(&hour); err != nil {
		return time.Time{}, typed(err, "read usage")
	}
	if !hour.Valid {
		return time.Time{}, nil
	}
	return fromMilli(hour.Int64), nil
}

// LastUsed is, for each key that has forwarded a request, the start of the
// latest hour in which it did. A key that was only refused is a key that was
// not used, so it is absent, as is a key with no events. It reads usage_hourly,
// so it is as fine as an hour.
func (tx *Tx) LastUsed() (map[string]time.Time, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT key_id, MAX(hour) FROM usage_hourly WHERE requests > 0 GROUP BY key_id`)
	if err != nil {
		return nil, typed(err, "read usage")
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var hour int64
		if err := rows.Scan(&id, &hour); err != nil {
			return nil, typed(err, "read usage")
		}
		out[id] = fromMilli(hour)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "read usage")
	}
	return out, nil
}

// ClientRow is one client address that used a key, from the events table: what
// that address did in the events kept, grouped. It holds no request content.
type ClientRow struct {
	IP string
	// Requests counts the forwarded requests and realtime sessions; Blocked the
	// requests kman refused.
	Requests, Blocked   int64
	FirstSeen, LastSeen time.Time
	// Country and ASN are what the databases said when the address was last
	// seen; empty and zero when there is no database, or it does not know.
	Country string
	ASN     int64
	// CostMicro adds the costs that are known, and Unmetered counts the
	// forwarded requests whose cost is not, so a total is never read as complete
	// when it is not.
	CostMicro int64
	Unmetered int64
}

// KeyClients is the addresses a key was used from, in the events kept, the most
// recently seen first, at most limit of them. total is how many addresses there
// are. A request that came in on the Unix socket has no address and is not
// counted.
func (tx *Tx) KeyClients(keyID string, limit int) (rows []ClientRow, total int, err error) {
	if err := tx.tx.QueryRowContext(tx.ctx, `SELECT COUNT(DISTINCT client_ip) FROM events WHERE key_id = ?1 AND client_ip IS NOT NULL`, keyID).Scan(&total); err != nil {
		return nil, 0, typed(err, "read clients")
	}
	res, err := tx.tx.QueryContext(tx.ctx, `SELECT client_ip,
			COALESCE(SUM(kind IN ('request', 'session')), 0), COALESCE(SUM(kind = 'blocked'), 0),
			MIN(ts), MAX(ts), COALESCE(MAX(country), ''), COALESCE(MAX(asn), 0),
			COALESCE(SUM(cost_micro), 0), COALESCE(SUM(kind IN ('request', 'session') AND cost_micro IS NULL), 0)
		FROM events WHERE key_id = ?1 AND client_ip IS NOT NULL
		GROUP BY client_ip ORDER BY MAX(ts) DESC, client_ip LIMIT ?2`, keyID, limit)
	if err != nil {
		return nil, 0, typed(err, "read clients")
	}
	defer res.Close()
	for res.Next() {
		var r ClientRow
		var first, last int64
		if err := res.Scan(&r.IP, &r.Requests, &r.Blocked, &first, &last, &r.Country, &r.ASN, &r.CostMicro, &r.Unmetered); err != nil {
			return nil, 0, typed(err, "read clients")
		}
		r.FirstSeen, r.LastSeen = fromMilli(first), fromMilli(last)
		rows = append(rows, r)
	}
	if err := res.Err(); err != nil {
		return nil, 0, typed(err, "read clients")
	}
	return rows, total, nil
}

// EventFilter picks events for ListEvents. A field left at its zero value
// filters nothing.
type EventFilter struct {
	KeyID string
	// Kinds limits the events to these kinds: request, blocked or session.
	Kinds []string
	// BlockedCode limits the events to those that carry this refusal code.
	BlockedCode string
	// Since is inclusive and Until is exclusive.
	Since, Until time.Time
	// BeforeID limits the events to those stored before the event with this id:
	// the cursor for the next, older page.
	BeforeID int64
}

// StoredEvent is an event as the events table holds it, with the id it is
// paged by and what is read from other tables to name it. It holds no request
// or response content.
type StoredEvent struct {
	ID int64
	EventRow
	// KeyName is the name of KeyID's key, empty for an event with no key or a
	// key with no name. EventRow.ASOrg is the label of the key's place for the
	// event's ASN, when it has one.
	KeyName string
}

// ListEvents returns at most limit events matching f, the newest first, which
// is the order they were stored in. A caller that wants to know whether there
// are more asks for one past its page.
func (tx *Tx) ListEvents(f EventFilter, limit int) ([]StoredEvent, error) {
	var where []string
	var args []any
	add := func(cond string, arg ...any) {
		where = append(where, cond)
		args = append(args, arg...)
	}
	if f.KeyID != "" {
		add("e.key_id = ?", f.KeyID)
	}
	if len(f.Kinds) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(f.Kinds)), ", ")
		kinds := make([]any, len(f.Kinds))
		for i, k := range f.Kinds {
			kinds[i] = k
		}
		add("e.kind IN ("+marks+")", kinds...)
	}
	if f.BlockedCode != "" {
		add("e.blocked_code = ?", f.BlockedCode)
	}
	if !f.Since.IsZero() {
		add("e.ts >= ?", toMilli(f.Since))
	}
	if !f.Until.IsZero() {
		add("e.ts < ?", toMilli(f.Until))
	}
	if f.BeforeID > 0 {
		add("e.id < ?", f.BeforeID)
	}
	query := `SELECT e.id, e.ts, COALESCE(e.key_id, ''), COALESCE(k.name, ''), e.kind, COALESCE(e.client_ip, ''), COALESCE(e.country, ''),
			COALESCE(e.asn, 0), COALESCE(p.label, ''), COALESCE(e.upstream_id, ''), COALESCE(e.model, ''), COALESCE(e.operation, ''),
			COALESCE(e.status, 0), COALESCE(e.blocked_code, ''), COALESCE(e.client, '')
		FROM events e
		LEFT JOIN virtual_keys k ON k.key_id = e.key_id
		LEFT JOIN baseline_places p ON p.key_id = e.key_id AND p.kind = 'asn' AND p.value = CAST(e.asn AS TEXT)`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := tx.tx.QueryContext(tx.ctx, query+" ORDER BY e.id DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, typed(err, "read events")
	}
	defer rows.Close()
	var out []StoredEvent
	for rows.Next() {
		var e StoredEvent
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.KeyID, &e.KeyName, &e.Kind, &e.ClientIP, &e.Country, &e.ASN, &e.ASOrg,
			&e.UpstreamID, &e.Model, &e.Operation, &e.Status, &e.BlockedCode, &e.Client); err != nil {
			return nil, typed(err, "read events")
		}
		e.Time = fromMilli(ts)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "read events")
	}
	return out, nil
}
