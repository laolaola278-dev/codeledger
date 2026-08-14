package cmd

import (
	"errors"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/store"
)

// withProjectLock acquires the project-level mutation lock for the given
// command, runs fn, and guarantees the lock is released (defer) afterwards.
//
// If the lock is already held by another process, a LOCK_CONFLICT typed error
// is returned and fn is never invoked.
//
// Release runs via defer on every path (callback success, callback error, and
// panics unwinding through here). A release failure is joined with any
// callback error (or returned on its own when the callback succeeded), so it
// is never printed as a side-channel warning and never silently turns a
// failed callback into exit 0. The callback error is joined FIRST so
// errors.As/Is keep classifying it: its stable exit code is preserved.
func withProjectLock(deps Dependencies, s *store.Store, command, agent, taskID string, fn func() error) (err error) {
	opts := store.ProjectLockOptions{
		Command:     command,
		Agent:       agent,
		TaskID:      taskID,
		TTL:         store.DefaultProjectLockTTL,
		AppendEvent: deps.LockAudit,
	}
	handle, aerr := store.AcquireProjectLock(s, opts)
	if aerr != nil {
		var ple *store.ProjectLockError
		if errors.As(aerr, &ple) {
			// Clear, actionable message for the operator/agent, classified as
			// a contention error so the process exits with code 3.
			return clierr.Wrap(clierr.KindLockConflict, ple,
				"project lock conflict: another agent is currently mutating .ctask state; "+
					"wait for it to finish, or run 'ctask locks' to inspect the lock")
		}
		return clierr.Wrap(clierr.KindOperation, aerr, "failed to acquire project lock")
	}
	// Acquire contract: a nil error guarantees a non-nil, held handle.
	defer func() {
		if rerr := handle.Release(); rerr != nil {
			err = errors.Join(err, clierr.Wrap(clierr.KindOperation, rerr, "failed to release project lock"))
		}
	}()
	return fn()
}
