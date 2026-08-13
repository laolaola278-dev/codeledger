package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

func newStartCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("start <task-id>", "Mark a task as in progress",
		`Set a task's status to in_progress.

The task's dependencies must all be completed before it can be started.`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "start", "", taskID, func() error {
			if err := service.StartTask(s, taskID); err != nil {
				return classifyErr("start failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Started task %s.\n", taskID)
			return nil
		})
	}
	return cmd
}
