package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// terminalPassphrase prompts on stderr and reads a passphrase from the
// terminal on stdin without echoing it. The caller wipes it.
func terminalPassphrase(prompt string, stderr io.Writer) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("the passphrase key source prompts on a terminal, and stdin is not one")
	}
	fmt.Fprint(stderr, prompt)
	p, err := term.ReadPassword(fd)
	fmt.Fprintln(stderr)
	return p, err
}

// terminalSecret reads a secret without echo when stdin is a terminal, and
// is nil otherwise, as when a token is piped in.
func terminalSecret(stderr io.Writer) func(prompt string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	return func(prompt string) ([]byte, error) {
		fmt.Fprint(stderr, prompt)
		p, err := term.ReadPassword(fd)
		fmt.Fprintln(stderr)
		return p, err
	}
}
