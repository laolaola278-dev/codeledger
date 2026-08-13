package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type heartbeatOptions struct {
	agent string
}

func newHeartbeatCmd(deps Dependencies) *cobra.Command {
	o := &heartbeatOptions{}
	cmd := newCommand("heartbeat <task-id>", "Send a heartbeat for a claimed task",
		`Update the heartbeat timestamp for a claimed task's lock.

This signals that the agent is still actively working on the task.
The heartbeat updates the heartbeat_at field in the lock entry.

Flags:
  --agent   Agent name sending the heartbeat`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if o.agent == "" {
			return clierr.New(clierr.KindUsage, "required flag(s) \"agent\" not set")
		}

		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "heartbeat", o.agent, taskID, func() error {
			if err := service.HeartbeatTask(s, taskID, o.agent); err != nil {
				return classifyErr("heartbeat failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Heartbeat sent for task %s.\n", taskID)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name sending the heartbeat")
	return cmd
}
