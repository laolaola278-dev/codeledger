package cmd

import (
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type listOptions struct {
	status string
}

func newListCmd(deps Dependencies) *cobra.Command {
	o := &listOptions{}
	cmd := newCommand("list", "List all tasks",
		`List all tasks, optionally filtered by status.

Flags:
  --status   Filter by status: pending, in_progress, done, blocked`)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		tasks, err := service.ListTasks(s, o.status)
		if err != nil {
			return classifyErr("list failed", err)
		}

		out := cmd.OutOrStdout()
		if len(tasks) == 0 {
			if o.status != "" {
				fmt.Fprintf(out, "No tasks with status '%s'.\n", o.status)
			} else {
				fmt.Fprintln(out, "No tasks. Use 'ctask add' to create one.")
			}
			return nil
		}

		// Header
		fmt.Fprintf(out, "%-10s %-12s %-8s %s\n", "ID", "Status", "Priority", "Title")
		fmt.Fprintln(out, strings.Repeat("-", 60))

		for _, t := range tasks {
			depStr := ""
			if len(t.DependsOn) > 0 {
				depStr = " [" + strings.Join(t.DependsOn, ",") + "]"
			}
			fmt.Fprintf(out, "%-10s %-12s %-8s %s%s\n", t.ID, t.Status, t.Priority, t.Title, depStr)
		}

		return nil
	}

	cmd.Flags().StringVar(&o.status, "status", "", "Filter by status: pending, in_progress, done, blocked")
	return cmd
}
