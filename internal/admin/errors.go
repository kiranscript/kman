package admin

import (
	"errors"
	"fmt"

	"github.com/kirank55/kman/internal/store"
)

// Code is the kind of an admin error, the same on every transport.
type Code string

const (
	// CodeInvalid is a request that breaks a rule: a bad name or value.
	CodeInvalid Code = "invalid"
	// CodeNotFound is a request for something that does not exist.
	CodeNotFound Code = "not_found"
	// CodeConflict is a request that clashes with the install's state, such
	// as a name already taken.
	CodeConflict Code = "conflict"
	// CodeUnauthorized is a request with no caller, or an admin token that
	// is unknown or revoked.
	CodeUnauthorized Code = "unauthorized"
)

// Error is an error the caller can act on. Its message is safe to show:
// it never holds a secret. Any other error from a command is internal.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// errUnauthorized does not say why a token was refused.
var errUnauthorized = &Error{Code: CodeUnauthorized, Message: "the admin token is unknown or revoked"}

// fromStore turns the store's typed errors into admin errors, describing
// the thing as what.
func fromStore(err error, what string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errorf(CodeNotFound, "%s does not exist", what)
	case errors.Is(err, store.ErrConflict):
		return errorf(CodeConflict, "%s already exists", what)
	}
	return err
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
