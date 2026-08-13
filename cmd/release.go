package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type releaseOptions struct {
	agent string
}

func newReleaseCmd(deps Dependencies) *cobra.Command {
	o := &releaseOptions{}
	cmd := newCommand("release <task-id>", "Release a claimed task",
		`Release a lock on a task. If the task is in_progress, it will be
set back to pending, making it available for other agents to claim.

If --agent is specified, only the lock held by that agent will be released.

Flags:
  --agent   Agent name to release (optional, releases any agent's lock if omitted)`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "release", o.agent, taskID, func() error {
			if err := service.ReleaseTask(s, taskID, o.agent); err != nil {
				return classifyErr("release failed", err)
			}
			if o.agent != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Released task %s from agent %s.\n", taskID, o.agent)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Released task %s.\n", taskID)
			}
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name to release (optional)")
	return cmd
}
