// Package admin is the one mutation path (ADR 0004). Each command runs one store
// transaction, publishes the change to the registry, and records an audit
// entry with its actor.
package admin
