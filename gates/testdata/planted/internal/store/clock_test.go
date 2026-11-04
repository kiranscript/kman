package store_test

import (
	"testing"
	"testing/synctest"
	"time"
)

// A test may read the clock, and a timer inside a synctest bubble is clean.
func TestTimerInBubble(t *testing.T) {
	_ = time.Now()
	synctest.Test(t, func(t *testing.T) { <-time.After(time.Second) })
}
