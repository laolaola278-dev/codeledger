package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

func newNoteCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("note <task-id> <note>", "Add a note to a task",
		`Append a note to a task without changing its status.

Use this to record findings, observations, or important context during work.`)
	cmd.Args = exactArgs(2)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		note := args[1]
		return withProjectLock(deps, s, "note", "", taskID, func() error {
			if err := service.NoteTask(s, taskID, note); err != nil {
				return classifyErr("note failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added note to task %s.\n", taskID)
			return nil
		})
	}
	return cmd
}
