// Package clierr defines the typed errors and stable exit-code contract used
// at the process boundary of the ctask CLI.
//
// Every error that can reach the process exit point must either be a *Error
// from this package or be wrapped by one. Classification is done with typed
// errors and errors.Is/As only - never by matching error strings.
package clierr

import (
	"errors"
	"fmt"
)

// Kind classifies a CLI failure into a stable machine-readable category.
// A Kind is the machine error code: it is embedded in JSON error output and
// mapped to a process exit code, and must never change between releases.
type Kind string

// Stable machine error codes. These values are part of the public contract
// (JSON error output and tests) and must not be renamed or renumbered.
const (
	KindUsage          Kind = "USAGE_ERROR"
	KindValidation     Kind = "VALIDATION_ERROR"
	KindNotInitialized Kind = "NOT_INITIALIZED"
	KindNotFound       Kind = "NOT_FOUND"
	KindCheckFailed    Kind = "CHECK_FAILED"
	KindLockConflict   Kind = "LOCK_CONFLICT"
	KindOperation      Kind = "OPERATION_FAILED"
	KindInternal       Kind = "INTERNAL_ERROR"
)

// Process exit codes. These are part of the public contract:
//
//	0 - success
//	1 - business execution failure or check/strict failure
//	2 - usage/validation failure
//	3 - contention/precondition (project mutation lock conflict)
const (
	ExitOK         = 0
	ExitBusiness   = 1
	ExitUsage      = 2
	ExitContention = 3
)

// Error is a typed CLI error carrying a stable machine kind and an optional
// underlying cause. The cause is preserved for errors.Is/As traversal.
type Error struct {
	Kind Kind
	Msg  string
	Err  error
}

// Error implements the error interface. The message is human-readable and
// keeps the wrapped cause text so operators can see the underlying failure.
func (e *Error) Error() string {
	switch {
	case e.Msg != "" && e.Err != nil:
		return e.Msg + ": " + e.Err.Error()
	case e.Msg != "":
		return e.Msg
	case e.Err != nil:
		return e.Err.Error()
	default:
		return "unknown error"
	}
}

// Unwrap returns the underlying cause, if any, so errors.Is/As can traverse
// the chain.
func (e *Error) Unwrap() error { return e.Err }

// New creates a typed CLI error without a cause.
func New(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Wrap creates a typed CLI error wrapping err as the cause. When format is
// empty, the rendered message is exactly the cause's message.
func Wrap(kind Kind, err error, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...), Err: err}
}

// KindOf returns the stable kind of err by walking the error chain with
// errors.As. The outermost *Error wins. Errors that are not typed by this
// package are classified as KindInternal, which is the explicit fallback for
// unknown errors.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// ExitCode maps an error to the stable process exit-code contract.
// nil maps to success (0); unknown errors fall back to ExitBusiness (1).
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	switch KindOf(err) {
	case KindUsage, KindValidation:
		return ExitUsage
	case KindLockConflict:
		return ExitContention
	default:
		return ExitBusiness
	}
}
