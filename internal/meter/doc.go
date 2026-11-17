// Package meter owns all spend: reservations, settlement, counters, spend periods,
// increment flushes, and failing closed. Soft-budget warnings count from what it
// has flushed, and are raised by alerts.
//
// Every key on a metered upstream has spend for the day and the month, capped or
// not, so that a cap added late counts what was spent before it. The counters
// live in memory and are changed with atomic operations, so the request path
// takes no lock. A Counters adapter keeps them: spend flushes to it as
// increments, and the month so far is read back from it at startup.
package meter
