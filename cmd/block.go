package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var blockCmd = &cobra.Command{
	Use:   "block <task-id> <reason>",
	Short: "Mark a task as blocked",
	Long: `Set a task's status to blocked with a reason.

The reason should explain what is blocking the task and what is needed to unblock it.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		taskID := args[0]
		reason := args[1]
		return withProjectLock(s, "block", "", taskID, func() error {
			if err := service.BlockTask(s, taskID, reason); err != nil {
				return fmt.Errorf("block failed: %w", err)
			}
			fmt.Printf("Blocked task %s.\n", taskID)
			return nil
		})
	},
}

func init() {
	rootCmd.AddCommand(blockCmd)
}
