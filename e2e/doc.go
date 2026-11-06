// Package e2e runs the real kman binary. TestMain builds it once from this
// checkout, the way a release does: CGO_ENABLED=0 and -trimpath.
//
// Each test gets its own home and data directory. behindKman starts the binary
// in front of a mock upstream that replays a transcript from
// testdata/transcripts and remembers what it was sent.
package e2e
