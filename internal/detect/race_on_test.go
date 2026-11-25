//go:build race

package detect_test

// raceDetector says the tests run under the race detector, which makes the
// store many times slower, so a test of how long the writer takes does not
// hold its budget.
const raceDetector = true
