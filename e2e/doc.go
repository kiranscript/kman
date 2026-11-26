// Package e2e runs the real kman binary. TestMain builds it once from this
// checkout, the way a release does: CGO_ENABLED=0 and -trimpath.
//
// Each test gets its own home and data directory. behindKman starts the binary
// in front of a mock upstream that replays a transcript from
// testdata/transcripts and remembers what it was sent.
//
// Every test calls t.Parallel: most of a test's time is spent waiting on the
// binary, such as for the store's 5-second batches, and the waits overlap. Two
// run alone, before the others start: TestCanaryDeadlineGate, which times
// alerts under its own load, and TestTheQuickstartRunsAsWritten, which runs
// the quickstart on ports it frees for kman to take.
package e2e
