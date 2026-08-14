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

	// P1 lease contract. These are the stable machine codes for the task
	// lease fencing protocol (see the P1 ADR). They are never derived from
	// error strings.
	KindLeaseConflict Kind = "LEASE_CONFLICT" // a new claim is blocked by an active lease (contention)
	KindLeaseRequired Kind = "LEASE_REQUIRED" // a lock record exists but --agent or --lease-id is missing
	KindLeaseMismatch Kind = "LEASE_MISMATCH" // --agent or --lease-id does not match the active lease
	KindLeaseExpired  Kind = "LEASE_EXPIRED"  // an ordinary operation targets an expired lease
	KindLeaseNotFound Kind = "LEASE_NOT_FOUND"

	// KindLegacyLeaseRequiresTakeover is returned for every ordinary
	// (non-force) operation that targets a pre-P1 legacy lock entry. Legacy
	// state is fail-closed and classified as a contention/precondition error
	// (exit 3), never a plain business failure.
	KindLegacyLeaseRequiresTakeover Kind = "LEGACY_LEASE_REQUIRES_TAKEOVER"

	// KindForceReasonRequired is returned when --force is used without a
	// non-empty --reason (validation failure, exit 2).
	KindForceReasonRequired Kind = "FORCE_REASON_REQUIRED"
	// KindForceAgentRequired is returned when --force is used without a
	// non-empty --agent actor (validation failure, exit 2).
	KindForceAgentRequired Kind = "FORCE_AGENT_REQUIRED"
)

// Process exit codes. These are part of the public contract:
//
//	0 - success
//	1 - business execution failure, check/strict failure, lease-not-found
//	2 - usage/validation failure, including --force without the required
//	    --reason or --agent actor
//	3 - contention/precondition: project mutation lock conflict, task lease
//	    conflict, missing/mismatched lease credentials, expired lease, or a
//	    legacy lock requiring explicit takeover
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
	case KindUsage, KindValidation, KindForceReasonRequired, KindForceAgentRequired:
		return ExitUsage
	case KindLockConflict, KindLeaseConflict, KindLeaseRequired, KindLeaseMismatch,
		KindLeaseExpired, KindLegacyLeaseRequiresTakeover:
		return ExitContention
	default:
		return ExitBusiness
	}
}
