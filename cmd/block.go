package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type blockOptions struct {
	agent   string
	leaseID string
	force   bool
	reason  string
}

func newBlockCmd(deps Dependencies) *cobra.Command {
	o := &blockOptions{}
	cmd := newCommand("block <task-id> <reason>", "Mark a task as blocked",
		`Set a task's status to blocked with a reason.

The reason should explain what is blocking the task and what is needed to unblock it.

Lease contract: if a lock record exists for the task, block requires the
exact owner (--agent + --lease-id) or --force --reason --agent. With no
record the compatibility path applies (flags optional).

Flags:
  --agent      Agent name (owner of the lease, if one exists)
  --lease-id   Lease ID (required when a lease exists)
  --force      Override an existing lease for this mutation (requires --reason and --agent)
  --reason     Human-readable reason required with --force`)
	cmd.Args = exactArgs(2)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		reason := args[1]
		return withProjectLock(deps, s, "block", o.agent, taskID, func() error {
			if err := service.BlockTask(s, deps.Clock, taskID, reason, lease.Auth{
				Agent:   o.agent,
				LeaseID: o.leaseID,
				Force:   o.force,
				Reason:  o.reason,
			}); err != nil {
				return classifyErr("block failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Blocked task %s.\n", taskID)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name (owner of the lease, if one exists)")
	cmd.Flags().StringVar(&o.leaseID, "lease-id", "", "Lease ID (required when a lease exists)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Override an existing lease for this mutation (requires --reason and --agent)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	return cmd
}
