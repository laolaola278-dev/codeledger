package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

func newBlockCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("block <task-id> <reason>", "Mark a task as blocked",
		`Set a task's status to blocked with a reason.

The reason should explain what is blocking the task and what is needed to unblock it.`)
	cmd.Args = exactArgs(2)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		reason := args[1]
		return withProjectLock(deps, s, "block", "", taskID, func() error {
			if err := service.BlockTask(s, taskID, reason); err != nil {
				return classifyErr("block failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Blocked task %s.\n", taskID)
			return nil
		})
	}
	return cmd
}
