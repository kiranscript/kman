// Package alerts delivers alerts through a persisted outbox with retries and the
// channel adapters.
//
// A producer calls Notify, or NotifyTx inside its own transaction. The alert is
// written to the outbox once for each channel that takes it, and Run delivers
// each channel's alerts on a goroutine of its own, so a channel that hangs
// delays no other (SPEC §9). The outbox is in the store, so alerts survive a
// restart. An alert holds what happened and what to type next, and never a
// secret or the content of a request (SPEC §1, invariant 3).
//
// Besides the audit log and the priority lane, alerts has the M1 warning
// producers (SPEC §9): Check raises spend thresholds, the monthly projection
// and key expiry from what the store holds, once per period, and RunWatch runs
// it on a timer; RunCredentialWarnings raises the warning for an upstream that
// refuses kman's credential. Time is an argument to Check, and the composition
// root hands in what alerts may not import: the policy reader and the settings.
package alerts
