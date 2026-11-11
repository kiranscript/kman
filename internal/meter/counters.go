package meter

import (
	"context"
	"sync"
)

// Row is one key's spend for one day, as the counters hold it: the amounts are
// increments when they are added, and totals when they are read.
type Row struct {
	KeyID string
	Day   string // 2026-11-11, in the spend timezone

	CostMicro, EstimatedMicro int64
	Requests, Unmetered       int64
}

// Counters is where spend is kept between runs. Adding increments rather than
// writing totals is what would let two instances share them without overwriting
// each other.
type Counters interface {
	// Since returns every row of a day not before day.
	Since(ctx context.Context, day string) ([]Row, error)
	// Add adds each row's amounts to the counter of its key and day, creating it.
	// All of the rows are added or none.
	Add(ctx context.Context, rows []Row) error
}

// Memory is the counters in memory, for tests.
type Memory struct {
	mu   sync.Mutex
	rows map[[2]string]Row
}

// NewMemory returns empty in-memory counters.
func NewMemory() *Memory { return &Memory{rows: map[[2]string]Row{}} }

// Since implements Counters.
func (m *Memory) Since(_ context.Context, day string) ([]Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Row
	for _, r := range m.rows {
		if r.Day >= day {
			out = append(out, r)
		}
	}
	return out, nil
}

// Add implements Counters.
func (m *Memory) Add(_ context.Context, rows []Row) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rows {
		k := [2]string{r.KeyID, r.Day}
		cur := m.rows[k]
		cur.KeyID, cur.Day = r.KeyID, r.Day
		cur.CostMicro += r.CostMicro
		cur.EstimatedMicro += r.EstimatedMicro
		cur.Requests += r.Requests
		cur.Unmetered += r.Unmetered
		m.rows[k] = cur
	}
	return nil
}
