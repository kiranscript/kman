package adminapi

import "time"

// SetKeepAlive makes a live stream say it is still there every d, and returns
// what puts it back.
func SetKeepAlive(d time.Duration) (restore func()) {
	old := keepAliveEvery
	keepAliveEvery = d
	return func() { keepAliveEvery = old }
}
