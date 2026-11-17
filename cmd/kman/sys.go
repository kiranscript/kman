package main

import (
	"io"
	"net/http"
	"time"
)

// sys is what a command reads from and writes to outside its arguments.
// main fills it with the real process; tests fill it with fakes.
type sys struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	now            func() time.Time
	goos           string
	// inContainer reports whether kman runs in a container.
	inContainer func() bool
	// passphrase prompts for a passphrase and reads it without echo.
	passphrase func(prompt string) ([]byte, error)
	stdin      io.Reader
	// readSecret prompts for a secret on a terminal and reads it without
	// echo. It is nil when stdin is not a terminal, so the CLI reads a
	// line of stdin instead.
	readSecret func(prompt string) ([]byte, error)
	// stopSignals restores the default handling of SIGINT and SIGTERM, so
	// a second signal during a graceful shutdown ends the process.
	stopSignals func()
	// http is the client for the two commands that may reach GitHub, version
	// --check and the opt-in update check. Nil is a default client; tests give
	// one that reaches a local server, so nothing in a test leaves the machine.
	http *http.Client
}
