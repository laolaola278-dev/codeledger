package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

type locksOptions struct {
	json bool
}

// locksCmdOutput is the JSON structure for `ctask locks --json`.
//
// `ctask locks` is a read-only snapshot command: it never acquires the
// project mutation lock and never writes to .ctask state. It only reads
// .ctask/locks.yaml (task locks) and .ctask/.ctask.lock (project mutation
// lock, if present). Output is therefore a point-in-time view and may be
// stale by the time it is displayed. Because no mutation lock is taken,
// two concurrent `ctask locks` calls (or a `ctask locks` running during a
// mutation) can never block each other.
type locksCmdOutput struct {
	TaskLocks     []taskLockSummary   `json:"task_locks,omitempty"`
	ProjectLock   *projectLockSummary `json:"project_lock,omitempty"`
	NoActiveLocks bool                `json:"no_active_locks,omitempty"`
}

type taskLockSummary struct {
	TaskID      string `json:"task_id"`
	Agent       string `json:"agent"`
	Role        string `json:"role,omitempty"`
	AcquiredAt  string `json:"acquired_at"`
	ExpiresAt   string `json:"expires_at"`
	HeartbeatAt string `json:"heartbeat_at,omitempty"`
	Expired     bool   `json:"expired,omitempty"`
}

type projectLockSummary struct {
	Pid       int    `json:"pid"`
	Command   string `json:"command"`
	Agent     string `json:"agent"`
	TaskID    string `json:"task_id"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	Expired   bool   `json:"expired,omitempty"`
}

func newLocksCmd(deps Dependencies) *cobra.Command {
	o := &locksOptions{}
	cmd := newCommand("locks", "Show current task locks and project mutation lock",
		`Display the current lock state of the project:

  - Task locks from .ctask/locks.yaml (claimed tasks)
  - The project-level mutation lock from .ctask/.ctask.lock (if held)

When no locks exist, prints "No active locks.".

Use --json for machine-readable output.

NOTE: this is a READ-ONLY snapshot command. It does NOT acquire the
project mutation lock and never modifies .ctask state, so it is always
safe to run even while another agent is mid-mutation. The output is a
point-in-time view and may change immediately afterwards.`)
	cmd.Args = noArgs()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		// Read-only snapshot: this command intentionally does NOT acquire the
		// project mutation lock (AcquireProjectLock) and does not write any
		// event. It only reads the lock files, so it can never conflict with a
		// concurrent mutation and can never leave state behind.

		out := locksCmdOutput{}

		// Task locks from locks.yaml (missing file => no locks).
		ll, lerr := s.ReadLocks()
		if lerr == nil {
			for _, l := range ll.Locks {
				out.TaskLocks = append(out.TaskLocks, taskLockSummary{
					TaskID:      l.TaskID,
					Agent:       l.Agent,
					Role:        l.Role,
					AcquiredAt:  l.AcquiredAt,
					ExpiresAt:   l.ExpiresAt,
					HeartbeatAt: l.HeartbeatAt,
					Expired:     l.IsExpired(),
				})
			}
		}

		// Project mutation lock from .ctask/.ctask.lock.
		pl, perr := store.ReadProjectLock(s)
		if perr != nil {
			return clierr.Wrap(clierr.KindOperation, perr, "failed to read project lock")
		}
		if pl != nil {
			out.ProjectLock = &projectLockSummary{
				Pid:       pl.Pid,
				Command:   pl.Command,
				Agent:     pl.Agent,
				TaskID:    pl.TaskID,
				CreatedAt: pl.CreatedAt,
				ExpiresAt: pl.ExpiresAt,
				Expired:   isProjectLockExpired(pl.ExpiresAt),
			}
		}

		stdout := cmd.OutOrStdout()
		if o.json {
			if out.ProjectLock == nil && len(out.TaskLocks) == 0 {
				out.NoActiveLocks = true
			}
			data, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to marshal JSON")
			}
			fmt.Fprintln(stdout, string(data))
			return nil
		}

		if out.ProjectLock == nil && len(out.TaskLocks) == 0 {
			fmt.Fprintln(stdout, "No active locks.")
			return nil
		}

		if out.ProjectLock != nil {
			pl := out.ProjectLock
			fmt.Fprintln(stdout, "Project mutation lock:")
			fmt.Fprintf(stdout, "  Pid:       %d\n", pl.Pid)
			fmt.Fprintf(stdout, "  Command:   %s\n", pl.Command)
			if pl.Agent != "" {
				fmt.Fprintf(stdout, "  Agent:     %s\n", pl.Agent)
			}
			if pl.TaskID != "" {
				fmt.Fprintf(stdout, "  Task:      %s\n", pl.TaskID)
			}
			fmt.Fprintf(stdout, "  Created:   %s\n", pl.CreatedAt)
			fmt.Fprintf(stdout, "  Expires:   %s\n", pl.ExpiresAt)
			if pl.Expired {
				fmt.Fprintln(stdout, "  Status:    EXPIRED (stale, will be reclaimed on next mutation)")
			}
			fmt.Fprintln(stdout)
		}

		if len(out.TaskLocks) > 0 {
			fmt.Fprintf(stdout, "Task locks (%d):\n", len(out.TaskLocks))
			for _, l := range out.TaskLocks {
				status := "active"
				if l.Expired {
					status = "expired"
				}
				fmt.Fprintf(stdout, "  %s  %s  agent=%s  expires=%s  [%s]\n", l.TaskID, l.Agent, l.Role, l.ExpiresAt, status)
			}
		}

		return nil
	}

	cmd.Flags().BoolVar(&o.json, "json", false, "Output locks as JSON")
	return cmd
}

// isProjectLockExpired reports whether a project lock timestamp is stale.
// A missing/unparseable timestamp is treated as expired.
func isProjectLockExpired(expiresAt string) bool {
	if expiresAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return time.Now().UTC().After(t)
}
