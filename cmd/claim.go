package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type claimOptions struct {
	agent  string
	role   string
	ttl    string
	force  bool
	reason string
	json   bool
}

// claimJSONOutput is the single JSON document emitted by `claim --json`.
type claimJSONOutput struct {
	TaskID               string `json:"task_id"`
	Agent                string `json:"agent"`
	LeaseID              string `json:"lease_id"`
	ExpiresAt            string `json:"expires_at"`
	LeaseDurationSeconds int64  `json:"lease_duration_seconds"`
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

An expired NEW-format lease on the same task is replaced in place (same-task
re-claim); a legacy lock requires --force --reason --agent to take over.

Flags:
  --agent   Agent name (e.g. codex, claude-code)
  --role    Role of the agent (e.g. developer, reviewer)
  --ttl     Lease duration (e.g. 120m, 2h)
  --force   Take over an existing active/expired/legacy record (requires --reason --agent)
  --reason  Human-readable reason required with --force
  --json    Output the claim as a single JSON document`)
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
			lock, err := service.ClaimTask(s, deps.Clock, deps.NewID, taskID, lease.Auth{
				Agent:  o.agent,
				Force:  o.force,
				Reason: o.reason,
			}, o.role, o.ttl)
			if err != nil {
				return classifyErr("claim failed", err)
			}

			if o.json {
				seconds := int64(0)
				if d, perr := time.ParseDuration(lock.LeaseDuration); perr == nil {
					seconds = int64(d.Seconds())
				}
				data, merr := json.MarshalIndent(claimJSONOutput{
					TaskID:               taskID,
					Agent:                o.agent,
					LeaseID:              lock.LeaseID,
					ExpiresAt:            lock.ExpiresAt,
					LeaseDurationSeconds: seconds,
				}, "", "  ")
				if merr != nil {
					return clierr.Wrap(clierr.KindOperation, merr, "failed to marshal JSON")
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Claimed task %s for agent %s.\n", taskID, o.agent)
			fmt.Fprintf(cmd.OutOrStdout(), "Lease ID: %s (expires %s)\n", lock.LeaseID, lock.ExpiresAt)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name (e.g. codex, claude-code)")
	cmd.Flags().StringVar(&o.role, "role", "developer", "Role of the agent (e.g. developer, reviewer)")
	cmd.Flags().StringVar(&o.ttl, "ttl", "120m", "Lease duration (e.g. 120m, 2h)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Take over an existing lease (requires --reason and --agent)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	cmd.Flags().BoolVar(&o.json, "json", false, "Output the claim as a single JSON document")
	return cmd
}
