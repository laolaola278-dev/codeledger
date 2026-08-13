package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start <task-id>",
	Short: "Mark a task as in progress",
	Long: `Set a task's status to in_progress.

The task's dependencies must all be completed before it can be started.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(s, "start", "", taskID, func() error {
			if err := service.StartTask(s, taskID); err != nil {
				return fmt.Errorf("start failed: %w", err)
			}
			fmt.Printf("Started task %s.\n", taskID)
			return nil
		})
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
