package cmd

import (
	"errors"
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/util"
	"github.com/spf13/cobra"
)

type addOptions struct {
	description string
	priority    string
	depends     string
}

func newAddCmd(deps Dependencies) *cobra.Command {
	o := &addOptions{}
	cmd := newCommand("add <title>", "Add a new task",
		`Add a new task to the project. The task will be assigned a unique ID like TASK-001.

Flags:
  --description   Task description
  --priority      Priority: low, medium (default), high
  --depends       Comma-separated list of task IDs this task depends on`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		title := args[0]
		depends := util.SplitCommas(o.depends)

		return withProjectLock(deps, s, "add", "", "", func() error {
			task, err := service.AddTask(s, title, o.description, o.priority, depends)
			if err != nil {
				if errors.Is(err, service.ErrInvalidPriority) {
					return clierr.Wrap(clierr.KindValidation, err, "add failed")
				}
				return classifyErr("add failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added task %s: %s\n", task.ID, task.Title)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.description, "description", "", "Task description")
	cmd.Flags().StringVar(&o.priority, "priority", "medium", "Priority: low, medium, high")
	cmd.Flags().StringVar(&o.depends, "depends", "", "Comma-separated dependency task IDs")
	return cmd
}
