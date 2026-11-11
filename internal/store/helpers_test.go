package store

import (
	"context"
	"testing"
	"time"
)

// testNow is the fixed time every test store opens at.
var testNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// openTest opens a store in dir and closes it when the test ends. Each test
// passes its own t.TempDir(), so each gets its own database.
func openTest(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(context.Background(), Options{Dir: dir, Now: testNow})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
