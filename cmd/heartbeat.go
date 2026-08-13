package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var heartbeatAgent string

var heartbeatCmd = &cobra.Command{
	Use:   "heartbeat <task-id>",
	Short: "Send a heartbeat for a claimed task",
	Long: `Update the heartbeat timestamp for a claimed task's lock.

This signals that the agent is still actively working on the task.
The heartbeat updates the heartbeat_at field in the lock entry.

Flags:
  --agent   Agent name sending the heartbeat`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(s, "heartbeat", heartbeatAgent, taskID, func() error {
			if err := service.HeartbeatTask(s, taskID, heartbeatAgent); err != nil {
				return fmt.Errorf("heartbeat failed: %w", err)
			}
			fmt.Printf("Heartbeat sent for task %s.\n", taskID)
			return nil
		})
	},
}

func init() {
	heartbeatCmd.Flags().StringVar(&heartbeatAgent, "agent", "", "Agent name sending the heartbeat")
	heartbeatCmd.MarkFlagRequired("agent")
	rootCmd.AddCommand(heartbeatCmd)
}
