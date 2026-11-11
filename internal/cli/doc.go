// Package cli is the kman command line: the commands and the one typed admin
// client. Like every transport, it imports only admin.
//
// The online commands (Commands) talk to a running kman through client,
// which implements admin.API over /admin/v1 with admin's request and
// response types. The offline commands (init, keygen, serve) live in
// cmd/kman, since they need modules cli may not import.
//
// Exit codes (SPEC §12): ExitOK, ExitError, and ExitFindings for a read
// command that found something. Every read command takes --json, which
// prints the admin API's response as it is.
package cli
