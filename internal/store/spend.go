package store

import "time"

// SpendRow is one key's spend for one day, from the spend_daily table. Day is
// the date in the spend timezone, such as 2026-11-11. When rows are added, the
// amounts are increments. Money is microdollars.
type SpendRow struct {
	KeyID string
	Day   string

	CostMicro      int64
	EstimatedMicro int64 // the part of CostMicro charged as an estimate
	Requests       int64
	Unmetered      int64 // requests forwarded whose cost is unknown
}

// AddSpend adds each row's amounts to the counters of its key and day,
// creating them. Spend is only ever added to, so two writers can't overwrite
// each other. A row for a key the store doesn't hold is left out rather than
// failing the rest.
func (tx *Tx) AddSpend(rows []SpendRow) error {
	for _, r := range rows {
		_, err := tx.tx.ExecContext(tx.ctx, `INSERT INTO spend_daily (key_id, day, cost_micro, estimated_micro, requests, unmetered_requests)
			SELECT ?1, ?2, ?3, ?4, ?5, ?6 WHERE EXISTS (SELECT 1 FROM virtual_keys WHERE key_id = ?1)
			ON CONFLICT (key_id, day) DO UPDATE SET
				cost_micro = cost_micro + excluded.cost_micro,
				estimated_micro = estimated_micro + excluded.estimated_micro,
				requests = requests + excluded.requests,
				unmetered_requests = unmetered_requests + excluded.unmetered_requests`,
			r.KeyID, r.Day, r.CostMicro, r.EstimatedMicro, r.Requests, r.Unmetered)
		if err != nil {
			return typed(err, "add spend of key "+r.KeyID)
		}
	}
	return nil
}

// SpendLabels are the labels spend_daily and budget_alerts keep a moment under,
// in the spend timezone loc: its day ("2026-11-04") and its month ("2026-11").
// The meter that writes spend and the alerts that read it both take their
// labels from here, so they never disagree about where a day ends.
func SpendLabels(t time.Time, loc *time.Location) (day, month string) {
	day = t.In(loc).Format(time.DateOnly)
	return day, day[:len("2006-01")]
}

// SpendSince returns every row whose day is not before day, by key and day.
func (tx *Tx) SpendSince(day string) ([]SpendRow, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT key_id, day, cost_micro, estimated_micro, requests, unmetered_requests
		FROM spend_daily WHERE day >= ? ORDER BY key_id, day`, day)
	if err != nil {
		return nil, typed(err, "read spend")
	}
	defer rows.Close()
	var out []SpendRow
	for rows.Next() {
		var r SpendRow
		if err := rows.Scan(&r.KeyID, &r.Day, &r.CostMicro, &r.EstimatedMicro, &r.Requests, &r.Unmetered); err != nil {
			return nil, typed(err, "read spend")
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "read spend")
	}
	return out, nil
}
