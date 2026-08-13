package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
	"github.com/spf13/cobra"
)

var (
	addDescription string
	addPriority    string
	addDepends     string
)

var addCmd = &cobra.Command{
	Use:   "add <title>",
	Short: "Add a new task",
	Long: `Add a new task to the project. The task will be assigned a unique ID like TASK-001.

Flags:
  --description   Task description
  --priority      Priority: low, medium (default), high
  --depends       Comma-separated list of task IDs this task depends on`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		title := args[0]
		depends := util.SplitCommas(addDepends)

		return withProjectLock(s, "add", "", "", func() error {
			task, err := service.AddTask(s, title, addDescription, addPriority, depends)
			if err != nil {
				return fmt.Errorf("add failed: %w", err)
			}
			fmt.Printf("Added task %s: %s\n", task.ID, task.Title)
			return nil
		})
	},
}

func init() {
	addCmd.Flags().StringVar(&addDescription, "description", "", "Task description")
	addCmd.Flags().StringVar(&addPriority, "priority", "medium", "Priority: low, medium, high")
	addCmd.Flags().StringVar(&addDepends, "depends", "", "Comma-separated dependency task IDs")
	rootCmd.AddCommand(addCmd)
}
