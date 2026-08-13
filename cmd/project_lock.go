package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/codeledger/codeledger/internal/store"
)

// withProjectLock acquires the project-level mutation lock for the given
// command, runs fn, and guarantees the lock is released (defer) afterwards.
//
// If the lock is already held by another process, a clear error is returned
// and fn is never invoked. The error text explains who holds the lock.
func withProjectLock(s *store.Store, command, agent, taskID string, fn func() error) error {
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
			// Clear, actionable message for the operator/agent.
			return fmt.Errorf("project lock conflict: another agent is currently mutating .ctask state "+
				"(pid %d, command %q, agent %q, task %q, expires %s). "+
				"Wait for it to finish, or run 'ctask locks' to inspect the lock.",
				ple.Lock.Pid, ple.Lock.Command, ple.Lock.Agent, ple.Lock.TaskID, ple.Lock.ExpiresAt)
		}
		return err
	}
	defer func() {
		if rerr := handle.Release(); rerr != nil {
			// Lock release is best-effort; surface via stderr so it is not lost.
			fmt.Fprintf(os.Stderr, "warning: failed to release project lock: %v\n", rerr)
		}
	}()

	return fn()
}
