package e2e

import (
	"testing"
	"time"
)

// Tests outside internal/ drive real processes and may wait on the clock.
func TestWaitsForAProcess(t *testing.T) { time.Sleep(time.Millisecond) }
