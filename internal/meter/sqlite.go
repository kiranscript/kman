package meter

import (
	"context"

	"github.com/kirank55/kman/internal/store"
)

// sqlite is the counters in the SQLite store: spend_daily, written only as
// increments.
type sqlite struct{ s *store.Store }

// SQLite returns counters kept in the store's spend_daily table.
func SQLite(s *store.Store) Counters { return sqlite{s} }

func (c sqlite) Since(ctx context.Context, day string) ([]Row, error) {
	var out []Row
	err := c.s.Do(ctx, func(tx *store.Tx) error {
		rows, err := tx.SpendSince(day)
		for _, r := range rows {
			out = append(out, Row(r))
		}
		return err
	})
	return out, err
}

func (c sqlite) Add(ctx context.Context, rows []Row) error {
	return c.s.Do(ctx, func(tx *store.Tx) error {
		add := make([]store.SpendRow, len(rows))
		for i, r := range rows {
			add[i] = store.SpendRow(r)
		}
		return tx.AddSpend(add)
	})
}
