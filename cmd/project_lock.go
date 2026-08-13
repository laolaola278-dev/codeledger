package cmd

import (
	"errors"
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/store"
)

// withProjectLock acquires the project-level mutation lock for the given
// command, runs fn, and guarantees the lock is released (defer) afterwards.
//
// If the lock is already held by another process, a LOCK_CONFLICT typed error
// is returned and fn is never invoked. Lock release failures are surfaced on
// the dependency stderr stream as warnings - never via os.Exit, so deferred
// cleanup always runs before the error reaches the process boundary.
func withProjectLock(deps Dependencies, s *store.Store, command, agent, taskID string, fn func() error) error {
	opts := store.ProjectLockOptions{
		Command: command,
		Agent:   agent,
		TaskID:  taskID,
		TTL:     store.DefaultProjectLockTTL,
	}
	handle, err := store.AcquireProjectLock(s, opts)
	if err != nil {
		var ple *store.ProjectLockError
		if errors.As(err, &ple) {
			// Clear, actionable message for the operator/agent, classified as
			// a contention error so the process exits with code 3.
			return clierr.Wrap(clierr.KindLockConflict, ple,
				"project lock conflict: another agent is currently mutating .ctask state; "+
					"wait for it to finish, or run 'ctask locks' to inspect the lock")
		}
		return clierr.Wrap(clierr.KindOperation, err, "failed to acquire project lock")
	}
	defer func() {
		if rerr := handle.Release(); rerr != nil {
			// Lock release is best-effort; surface via stderr so it is not lost.
			fmt.Fprintf(deps.Stderr, "warning: failed to release project lock: %v\n", rerr)
		}
	}()

	return fn()
}
