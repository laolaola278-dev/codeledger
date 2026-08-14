package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type claimOptions struct {
	agent string
	role  string
	ttl   string
}

func newClaimCmd(deps Dependencies) *cobra.Command {
	o := &claimOptions{}
	cmd := newCommand("claim <task-id>", "Claim a task for an agent",
		`Claim a task and lock it for a specific agent.

This prevents other agents from picking up the same task. The task's status
will be set to in_progress, and a lease entry will be created in locks.yaml.

Every claim creates a unique lease (lease_id) with a fixed duration. The
lease owner can renew it with 'ctask heartbeat' and must release it with
'ctask release' when done.

Flags:
  --agent   Agent name (e.g. codex, claude-code)
  --role    Role of the agent (e.g. developer, reviewer)
  --ttl     Lease duration (e.g. 120m, 2h)`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if o.agent == "" {
			return clierr.New(clierr.KindUsage, "required flag(s) \"agent\" not set")
		}

		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "claim", o.agent, taskID, func() error {
			lock, err := service.ClaimTask(s, deps.Clock, deps.NewID, taskID, o.agent, o.role, o.ttl)
			if err != nil {
				return classifyErr("claim failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Claimed task %s for agent %s.\n", taskID, o.agent)
			fmt.Fprintf(cmd.OutOrStdout(), "Lease ID: %s (expires %s)\n", lock.LeaseID, lock.ExpiresAt)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name (e.g. codex, claude-code)")
	cmd.Flags().StringVar(&o.role, "role", "developer", "Role of the agent (e.g. developer, reviewer)")
	cmd.Flags().StringVar(&o.ttl, "ttl", "120m", "Lease duration (e.g. 120m, 2h)")
	return cmd
}
