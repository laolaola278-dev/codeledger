package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type startOptions struct {
	agent   string
	leaseID string
	force   bool
	reason  string
}

func newStartCmd(deps Dependencies) *cobra.Command {
	o := &startOptions{}
	cmd := newCommand("start <task-id>", "Mark a task as in progress",
		`Set a task's status to in_progress.

The task's dependencies must all be completed before it can be started.

Lease contract: if a lock record exists for the task, start requires the
exact owner (--agent + --lease-id) or --force --reason --agent. With no
record the compatibility path applies (flags optional).

Flags:
  --agent      Agent name (owner of the lease, if one exists)
  --lease-id   Lease ID (required when a lease exists)
  --force      Override an existing lease for this mutation (requires --reason and --agent)
  --reason     Human-readable reason required with --force`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "start", o.agent, taskID, func() error {
			if err := service.StartTask(s, deps.Clock, taskID, lease.Auth{
				Agent:   o.agent,
				LeaseID: o.leaseID,
				Force:   o.force,
				Reason:  o.reason,
			}); err != nil {
				return classifyErr("start failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Started task %s.\n", taskID)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name (owner of the lease, if one exists)")
	cmd.Flags().StringVar(&o.leaseID, "lease-id", "", "Lease ID (required when a lease exists)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Override an existing lease for this mutation (requires --reason and --agent)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	return cmd
}
