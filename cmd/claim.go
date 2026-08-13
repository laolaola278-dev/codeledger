package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	claimAgent string
	claimRole  string
	claimTTL   string
)

var claimCmd = &cobra.Command{
	Use:   "claim <task-id>",
	Short: "Claim a task for an agent",
	Long: `Claim a task and lock it for a specific agent.

This prevents other agents from picking up the same task. The task's status
will be set to in_progress, and a lock entry will be created in locks.yaml.

Flags:
  --agent   Agent name (e.g. codex, claude-code)
  --role    Role of the agent (e.g. developer, reviewer)
  --ttl     Time-to-live for the lock (e.g. 120m, 2h)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(s, "claim", claimAgent, taskID, func() error {
			if err := service.ClaimTask(s, taskID, claimAgent, claimRole, claimTTL); err != nil {
				return fmt.Errorf("claim failed: %w", err)
			}
			fmt.Printf("Claimed task %s for agent %s.\n", taskID, claimAgent)
			return nil
		})
	},
}

func init() {
	claimCmd.Flags().StringVar(&claimAgent, "agent", "", "Agent name (e.g. codex, claude-code)")
	claimCmd.Flags().StringVar(&claimRole, "role", "developer", "Role of the agent (e.g. developer, reviewer)")
	claimCmd.Flags().StringVar(&claimTTL, "ttl", "120m", "Time-to-live for the lock (e.g. 120m, 2h)")
	claimCmd.MarkFlagRequired("agent")
	rootCmd.AddCommand(claimCmd)
}
