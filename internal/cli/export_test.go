package cli

import "time"

// SetExportPage makes audit export ask for n entries at a time, and returns
// a function that restores the page size.
func SetExportPage(n int) func() {
	old := exportPage
	exportPage = n
	return func() { exportPage = old }
}

// SetMissedEvery makes events --follow check for drops every d, and returns a
// function that restores it.
func SetMissedEvery(d time.Duration) func() {
	old := missedEvery
	missedEvery = d
	return func() { missedEvery = old }
}
