// Package pricing holds the price table and price overrides, and turns a request
// into an estimate and a response into a cost.
//
// The table is embedded in the binary and changes only with a release
// (ADR 0007): nothing here reaches the network, and how old the table is never
// changes what it answers.
package pricing
