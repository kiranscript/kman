// Package alerts delivers alerts through a persisted outbox with retries and the
// channel adapters.
//
// A producer calls Notify, or NotifyTx inside its own transaction. The alert is
// written to the outbox once for each channel that takes it, and Run delivers
// each channel's alerts on a goroutine of its own, so a channel that hangs
// delays no other (SPEC §9). The outbox is in the store, so alerts survive a
// restart. An alert holds what happened and what to type next, and never a
// secret or the content of a request (SPEC §1, invariant 3).
package alerts
