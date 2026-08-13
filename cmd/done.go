package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	doneFiles       string
	doneTest        string
	doneResult      string
	doneNote        string
	doneAutoFiles   bool
	doneCaptureDiff bool
)

var doneCmd = &cobra.Command{
	Use:   "done <task-id>",
	Short: "Mark a task as completed",
	Long: `Mark a task as done with optional metadata.

Flags:
  --files          Comma-separated list of modified files
  --test           Test command that was run
  --result         Test result: passed, failed, skipped, unknown
  --note           Completion note
  --auto-files     Automatically detect changed files from Git (default: false)
  --capture-diff   Capture full Git diff in evidence file (default: false)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(s, "done", "", taskID, func() error {
			if err := service.CompleteTask(s, taskID, doneFiles, doneTest, doneResult, doneNote, doneAutoFiles, doneCaptureDiff); err != nil {
				return fmt.Errorf("done failed: %w", err)
			}
			fmt.Printf("Completed task %s.\n", taskID)
			return nil
		})
	},
}

func init() {
	doneCmd.Flags().StringVar(&doneFiles, "files", "", "Comma-separated list of modified files")
	doneCmd.Flags().StringVar(&doneTest, "test", "", "Test command that was run")
	doneCmd.Flags().StringVar(&doneResult, "result", "", "Test result: passed, failed, skipped, unknown")
	doneCmd.Flags().StringVar(&doneNote, "note", "", "Completion note")
	doneCmd.Flags().BoolVar(&doneAutoFiles, "auto-files", false, "Automatically detect changed files from Git")
	doneCmd.Flags().BoolVar(&doneCaptureDiff, "capture-diff", false, "Capture full Git diff in evidence file")
	rootCmd.AddCommand(doneCmd)
}
