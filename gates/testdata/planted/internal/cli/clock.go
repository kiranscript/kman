package cli

import clock "time"

func age(t clock.Time) clock.Duration { return clock.Since(t) }

// The clock in a string is not a clock read: "time.Now()".
func until(t clock.Time) clock.Duration { return t.Sub(t) }
