package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/lease"
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
  - an active lease can only be released by its exact owner: both --agent and
    --lease-id must match the lease (the fencing token is mandatory);
  - to break another agent's lease, pass --force with an explicit --reason and
    a non-empty --agent actor;
  - legacy locks (pre-lease format) are fail-closed: release them with
    --force --reason --agent as well;
  - an expired lease is fail-closed too (LEASE_EXPIRED): ordinary release
    never cleans it - re-claim the task or force takeover instead.

Flags:
  --agent      Agent name (owner of the lease being released)
  --lease-id   Lease ID to release (required when a lease exists)
  --force      Break the lease even when not the owner (requires --reason and --agent)
  --reason     Human-readable reason required with --force`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "release", o.agent, taskID, func() error {
			if err := service.ReleaseTask(s, deps.Clock, taskID, lease.Auth{
				Agent:   o.agent,
				LeaseID: o.leaseID,
				Force:   o.force,
				Reason:  o.reason,
			}); err != nil {
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
	cmd.Flags().StringVar(&o.leaseID, "lease-id", "", "Lease ID to release (required when a lease exists)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Break the lease even when not the owner (requires --reason and --agent)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	return cmd
}
