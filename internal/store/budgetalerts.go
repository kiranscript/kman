package store

import "time"

// UnmarkBudgetAlert takes back a MarkBudgetAlert, in the same transaction, for a
// warning that no channel received: it is still owed.
func (tx *Tx) UnmarkBudgetAlert(keyID, period string, threshold int) error {
	if _, err := tx.tx.ExecContext(tx.ctx, `DELETE FROM budget_alerts WHERE key_id = ? AND period = ? AND threshold = ?`, keyID, period, threshold); err != nil {
		return typed(err, "take back warning for key "+keyID)
	}
	return nil
}

// MarkBudgetAlert records that the warning for (key, period, threshold) was
// sent at time at, and says whether this is the first time it was: only the
// first is to be sent. It is how a warning alerts once per period, and it
// survives a restart. The period and threshold say what the warning is about:
// a day (2026-11-11), a month (2026-11), or, for a warning that is not about
// spend, a label of the producer's own that cannot be mistaken for either. The
// key must exist.
func (tx *Tx) MarkBudgetAlert(keyID, period string, threshold int, at time.Time) (first bool, err error) {
	res, err := tx.tx.ExecContext(tx.ctx, `INSERT OR IGNORE INTO budget_alerts (key_id, period, threshold, sent_at) VALUES (?, ?, ?, ?)`,
		keyID, period, threshold, toMilli(at))
	if err != nil {
		return false, typed(err, "record warning for key "+keyID)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, typed(err, "record warning for key "+keyID)
	}
	return n == 1, nil
}
