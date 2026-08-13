package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var releaseAgent string

var releaseCmd = &cobra.Command{
	Use:   "release <task-id>",
	Short: "Release a claimed task",
	Long: `Release a lock on a task. If the task is in_progress, it will be
set back to pending, making it available for other agents to claim.

If --agent is specified, only the lock held by that agent will be released.

Flags:
  --agent   Agent name to release (optional, releases any agent's lock if omitted)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(s, "release", releaseAgent, taskID, func() error {
			if err := service.ReleaseTask(s, taskID, releaseAgent); err != nil {
				return fmt.Errorf("release failed: %w", err)
			}
			if releaseAgent != "" {
				fmt.Printf("Released task %s from agent %s.\n", taskID, releaseAgent)
			} else {
				fmt.Printf("Released task %s.\n", taskID)
			}
			return nil
		})
	},
}

func init() {
	releaseCmd.Flags().StringVar(&releaseAgent, "agent", "", "Agent name to release (optional)")
	rootCmd.AddCommand(releaseCmd)
}
