// Package policy decides whether a request may proceed: allowlists, locks, model
// rules and the unmetered rule. Evaluate is a pure function of the request's
// facts and the key's policy, and never reads the clock.
package policy
