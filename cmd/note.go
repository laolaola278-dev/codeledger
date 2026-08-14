package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type noteOptions struct {
	agent   string
	leaseID string
	force   bool
	reason  string
}

func newNoteCmd(deps Dependencies) *cobra.Command {
	o := &noteOptions{}
	cmd := newCommand("note <task-id> <note>", "Add a note to a task",
		`Append a note to a task without changing its status.

Use this to record findings, observations, or important context during work.

Lease contract: if a lock record exists for the task, note requires the
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
		note := args[1]
		return withProjectLock(deps, s, "note", o.agent, taskID, func() error {
			if err := service.NoteTask(s, deps.Clock, taskID, note, lease.Auth{
				Agent:   o.agent,
				LeaseID: o.leaseID,
				Force:   o.force,
				Reason:  o.reason,
			}); err != nil {
				return classifyErr("note failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added note to task %s.\n", taskID)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name (owner of the lease, if one exists)")
	cmd.Flags().StringVar(&o.leaseID, "lease-id", "", "Lease ID (required when a lease exists)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Override an existing lease for this mutation (requires --reason and --agent)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	return cmd
}
