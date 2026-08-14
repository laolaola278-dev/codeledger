package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type releaseOptions struct {
	agent   string
	leaseID string
	force   bool
	reason  string
}

func newReleaseCmd(deps Dependencies) *cobra.Command {
	o := &releaseOptions{}
	cmd := newCommand("release <task-id>", "Release a claimed task",
		`Release the lease on a task. If the task is in_progress, it will be
set back to pending, making it available for other agents to claim.

Lease contract:
  - an active lease can only be released by its owner: pass --agent matching
    the lease owner (and --lease-id matching the lease when provided);
  - to break another agent's lease, pass --force with an explicit --reason;
  - legacy locks (pre-lease format) are fail-closed: release them with
    --force --reason as well;
  - an expired lease is stale and can be cleaned by anyone without force.

Flags:
  --agent      Agent name (owner of the lease being released)
  --lease-id   Lease ID to release (optional; verified against the lease)
  --force      Break the lease even when not the owner (requires --reason)
  --reason     Human-readable reason required with --force`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "release", o.agent, taskID, func() error {
			if err := service.ReleaseTask(s, deps.Clock, taskID, o.agent, o.leaseID, o.force, o.reason); err != nil {
				return classifyErr("release failed", err)
			}
			if o.agent != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Released task %s from agent %s.\n", taskID, o.agent)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Released task %s.\n", taskID)
			}
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name (owner of the lease being released)")
	cmd.Flags().StringVar(&o.leaseID, "lease-id", "", "Lease ID to release (verified against the lease)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Break the lease even when not the owner (requires --reason)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	return cmd
}
