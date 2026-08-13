package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var noteCmd = &cobra.Command{
	Use:   "note <task-id> <note>",
	Short: "Add a note to a task",
	Long: `Append a note to a task without changing its status.

Use this to record findings, observations, or important context during work.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		taskID := args[0]
		note := args[1]
		return withProjectLock(s, "note", "", taskID, func() error {
			if err := service.NoteTask(s, taskID, note); err != nil {
				return fmt.Errorf("note failed: %w", err)
			}
			fmt.Printf("Added note to task %s.\n", taskID)
			return nil
		})
	},
}

func init() {
	rootCmd.AddCommand(noteCmd)
}
