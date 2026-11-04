package main

import (
	"io"
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
	// stopSignals restores the default handling of SIGINT and SIGTERM, so
	// a second signal during a graceful shutdown ends the process.
	stopSignals func()
}
