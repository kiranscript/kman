package store

import (
	"database/sql"
	"time"
)

// AlertChannel is where alerts are delivered, as stored: Slack or a signed
// webhook. Its address and signing secret are sealed by the vault in
// ConfigCiphertext, so the row holds nothing secret without the data key.
// Only live channels are returned.
type AlertChannel struct {
	ID               string // "ch_" plus 12 base62, from NewID: the sealing binds it
	Name             string // unique among live channels
	Kind             string // "slack" or "webhook"
	ConfigCiphertext []byte
	Nonce            []byte
	DataKeyID        string
	// DisplayHost is the host of the address, which is all of it that may be
	// shown: a Slack address is itself a secret.
	DisplayHost string
	// MinSeverity is the lowest severity the channel takes: low, medium, high or critical.
	MinSeverity string
	// LifecycleAlerts is whether the channel also takes spend and lifecycle warnings.
	LifecycleAlerts bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Generation      int64 // set by the store
}

const alertChannelColumns = `id, name, kind, config_ciphertext, nonce, dek_id, display_host, min_severity,
	lifecycle_alerts, created_at, updated_at, generation`

// CreateAlertChannel inserts c with this transaction's generation and returns
// it as stored. The id is the caller's, since sealing binds it. A live channel
// with the same name is ErrConflict.
func (tx *Tx) CreateAlertChannel(c AlertChannel) (AlertChannel, error) {
	gen, err := tx.Generation()
	if err != nil {
		return AlertChannel{}, err
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO alert_channels (`+alertChannelColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+alertChannelColumns,
		c.ID, c.Name, c.Kind, c.ConfigCiphertext, c.Nonce, c.DataKeyID, c.DisplayHost, c.MinSeverity,
		c.LifecycleAlerts, toMilli(c.CreatedAt), toMilli(c.UpdatedAt), gen)
	return scanAlertChannel(row, "create alert channel "+c.Name)
}

// AlertChannel returns the live channel with this id, or ErrNotFound.
func (tx *Tx) AlertChannel(id string) (AlertChannel, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+alertChannelColumns+` FROM alert_channels WHERE id = ? AND deleted_at IS NULL`, id)
	return scanAlertChannel(row, "alert channel "+id)
}

// AlertChannelByName returns the live channel with this name, or ErrNotFound.
func (tx *Tx) AlertChannelByName(name string) (AlertChannel, error) {
	row := tx.tx.QueryRowContext(tx.ctx,
		`SELECT `+alertChannelColumns+` FROM alert_channels WHERE name = ? AND deleted_at IS NULL`, name)
	return scanAlertChannel(row, "alert channel "+name)
}

// AlertChannels returns every live channel, by name.
func (tx *Tx) AlertChannels() ([]AlertChannel, error) {
	rows, err := tx.tx.QueryContext(tx.ctx,
		`SELECT `+alertChannelColumns+` FROM alert_channels WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, typed(err, "list alert channels")
	}
	defer rows.Close()
	var out []AlertChannel
	for rows.Next() {
		c, err := scanAlertChannel(rows, "list alert channels")
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list alert channels")
	}
	return out, nil
}

// DeleteAlertChannel soft-deletes the live channel with this id at time at,
// freeing its name and wiping its sealed config, and gives up the alerts still
// waiting for it. A missing or already deleted channel is ErrNotFound.
func (tx *Tx) DeleteAlertChannel(id string, at time.Time) error {
	gen, err := tx.Generation()
	if err != nil {
		return err
	}
	var got string
	err = tx.tx.QueryRowContext(tx.ctx, `UPDATE alert_channels SET deleted_at = ?, updated_at = ?, generation = ?,
		config_ciphertext = x'', nonce = x'' WHERE id = ? AND deleted_at IS NULL RETURNING id`,
		toMilli(at), toMilli(at), gen, id).Scan(&got)
	if err != nil {
		return typed(err, "delete alert channel "+id)
	}
	_, err = tx.tx.ExecContext(tx.ctx, `UPDATE alert_outbox SET failed_at = ?, last_error = 'the channel was removed'
		WHERE channel_id = ? AND delivered_at IS NULL AND failed_at IS NULL`, toMilli(at), id)
	if err != nil {
		return typed(err, "give up alerts for channel "+id)
	}
	return nil
}

func scanAlertChannel(row scanner, what string) (AlertChannel, error) {
	var c AlertChannel
	var created, updated int64
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.ConfigCiphertext, &c.Nonce, &c.DataKeyID, &c.DisplayHost, &c.MinSeverity,
		&c.LifecycleAlerts, &created, &updated, &c.Generation)
	if err != nil {
		return AlertChannel{}, typed(err, what)
	}
	c.CreatedAt, c.UpdatedAt = fromMilli(created), fromMilli(updated)
	return c, nil
}

// OutboxAlert is one alert waiting in the outbox for one channel. The outbox is
// persisted, so an alert survives a restart until it is delivered or given up.
type OutboxAlert struct {
	ID          int64 // set by EnqueueAlert
	ChannelID   string
	Kind        string // what raised it, such as "audit"
	IncidentID  int64  // 0 for none
	Priority    bool   // taken before ordinary alerts
	PayloadJSON string // what the channel is sent, from the alert; never a secret
	CreatedAt   time.Time
	Attempts    int
	// NextAttemptAt is when the alert is next due; EnqueueAlert makes it
	// CreatedAt when zero.
	NextAttemptAt time.Time
	DeliveredAt   time.Time // zero until delivered
	FailedAt      time.Time // zero unless given up
	LastError     string
}

const outboxColumns = `id, channel_id, kind, incident_id, priority, payload_json, created_at, attempts,
	next_attempt_at, delivered_at, failed_at, last_error`

// EnqueueAlert adds a to the outbox and returns it with its id.
func (tx *Tx) EnqueueAlert(a OutboxAlert) (OutboxAlert, error) {
	if a.NextAttemptAt.IsZero() {
		a.NextAttemptAt = a.CreatedAt
	}
	row := tx.tx.QueryRowContext(tx.ctx, `INSERT INTO alert_outbox (channel_id, kind, incident_id, priority, payload_json,
		created_at, next_attempt_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING `+outboxColumns,
		a.ChannelID, a.Kind, nullInt(a.IncidentID), a.Priority, a.PayloadJSON, toMilli(a.CreatedAt), toMilli(a.NextAttemptAt))
	return scanOutbox(row, "enqueue alert")
}

// DueAlerts returns up to limit alerts for a channel that are due at now,
// neither delivered nor given up: priority alerts first, then oldest first.
func (tx *Tx) DueAlerts(channelID string, now time.Time, limit int) ([]OutboxAlert, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT `+outboxColumns+` FROM alert_outbox
		WHERE channel_id = ? AND delivered_at IS NULL AND failed_at IS NULL AND next_attempt_at <= ?
		ORDER BY priority DESC, id LIMIT ?`, channelID, toMilli(now), limit)
	if err != nil {
		return nil, typed(err, "list due alerts")
	}
	defer rows.Close()
	var out []OutboxAlert
	for rows.Next() {
		a, err := scanOutbox(rows, "list due alerts")
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list due alerts")
	}
	return out, nil
}

// NextAlertDue is when the soonest alert still waiting for a channel is due.
// It reports false when none is waiting.
func (tx *Tx) NextAlertDue(channelID string) (time.Time, bool, error) {
	var next sql.NullInt64
	err := tx.tx.QueryRowContext(tx.ctx, `SELECT min(next_attempt_at) FROM alert_outbox
		WHERE channel_id = ? AND delivered_at IS NULL AND failed_at IS NULL`, channelID).Scan(&next)
	if err != nil {
		return time.Time{}, false, typed(err, "next due alert")
	}
	if !next.Valid {
		return time.Time{}, false, nil
	}
	return fromMilli(next.Int64), true, nil
}

// AlertDelivered records that alert id reached its channel at time at.
func (tx *Tx) AlertDelivered(id int64, at time.Time) error {
	return tx.markAlert(id, `UPDATE alert_outbox SET delivered_at = ?, attempts = attempts + 1, last_error = NULL WHERE id = ?`, toMilli(at))
}

// AlertRetry records a failed attempt at alert id, to be tried again at next.
func (tx *Tx) AlertRetry(id int64, next time.Time, reason string) error {
	return tx.markAlert(id, `UPDATE alert_outbox SET attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE id = ?`,
		toMilli(next), reason)
}

// AlertFailed records that alert id is given up at time at.
func (tx *Tx) AlertFailed(id int64, at time.Time, reason string) error {
	return tx.markAlert(id, `UPDATE alert_outbox SET attempts = attempts + 1, failed_at = ?, last_error = ? WHERE id = ?`,
		toMilli(at), reason)
}

func (tx *Tx) markAlert(id int64, query string, args ...any) error {
	res, err := tx.tx.ExecContext(tx.ctx, query, append(args, id)...)
	if err != nil {
		return typed(err, "update alert")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return typed(sql.ErrNoRows, "alert")
	}
	return nil
}

// OutboxCount is what the outbox holds for one channel.
type OutboxCount struct {
	Pending, Failed, Delivered int
	// LastError is why the most recent attempt that failed did, "" if none has.
	LastError string
}

// OutboxStats counts the outbox by channel.
func (tx *Tx) OutboxStats() (map[string]OutboxCount, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT channel_id,
		count(*) FILTER (WHERE delivered_at IS NULL AND failed_at IS NULL),
		count(*) FILTER (WHERE failed_at IS NOT NULL),
		count(*) FILTER (WHERE delivered_at IS NOT NULL),
		coalesce((SELECT last_error FROM alert_outbox o WHERE o.channel_id = alert_outbox.channel_id
			AND o.last_error IS NOT NULL ORDER BY o.id DESC LIMIT 1), '')
		FROM alert_outbox GROUP BY channel_id`)
	if err != nil {
		return nil, typed(err, "count the outbox")
	}
	defer rows.Close()
	out := map[string]OutboxCount{}
	for rows.Next() {
		var id string
		var c OutboxCount
		if err := rows.Scan(&id, &c.Pending, &c.Failed, &c.Delivered, &c.LastError); err != nil {
			return nil, typed(err, "count the outbox")
		}
		out[id] = c
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "count the outbox")
	}
	return out, nil
}

func scanOutbox(row scanner, what string) (OutboxAlert, error) {
	var a OutboxAlert
	var incident, delivered, failed sql.NullInt64
	var created, next int64
	var lastErr sql.NullString
	err := row.Scan(&a.ID, &a.ChannelID, &a.Kind, &incident, &a.Priority, &a.PayloadJSON, &created, &a.Attempts,
		&next, &delivered, &failed, &lastErr)
	if err != nil {
		return OutboxAlert{}, typed(err, what)
	}
	a.IncidentID, a.CreatedAt, a.NextAttemptAt, a.LastError = incident.Int64, fromMilli(created), fromMilli(next), lastErr.String
	if delivered.Valid {
		a.DeliveredAt = fromMilli(delivered.Int64)
	}
	if failed.Valid {
		a.FailedAt = fromMilli(failed.Int64)
	}
	return a, nil
}

// AlertChannelConfig is what a channel's ConfigCiphertext seals, as JSON: its
// address and, for a webhook, the secret it signs with. admin seals it and
// alerts opens it, so both share this type.
type AlertChannelConfig struct {
	URL           string `json:"url"`
	SigningSecret string `json:"signing_secret,omitempty"`
}
