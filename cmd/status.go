package cmd

import (
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

func newStatusCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("status", "Show project status overview",
		`Display a summary of the project's current status:
  - Total / pending / in_progress / done / blocked task counts
  - Current in-progress task
  - Blocked tasks with reasons
  - Next suggested task to work on`)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		status, err := service.GetStatus(s)
		if err != nil {
			return classifyErr("status failed", err)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Project: %s\n\n", status.ProjectName)
		fmt.Fprintln(out, "Progress:")
		fmt.Fprintf(out, "  Done:       %d\n", status.Done)
		fmt.Fprintf(out, "  In Progress: %d\n", status.InProgress)
		fmt.Fprintf(out, "  Pending:    %d\n", status.Pending)
		fmt.Fprintf(out, "  Blocked:    %d\n", status.Blocked)
		fmt.Fprintln(out)

		if status.CurrentTask != nil {
			fmt.Fprintln(out, "Current Task:")
			fmt.Fprintf(out, "  %s: %s\n", status.CurrentTask.ID, status.CurrentTask.Title)
			fmt.Fprintln(out)
		}

		if len(status.BlockedTasks) > 0 {
			fmt.Fprintln(out, "Blocked Tasks:")
			for _, t := range status.BlockedTasks {
				fmt.Fprintf(out, "  %s: %s\n", t.ID, t.Title)
				if t.BlockedReason != "" {
					fmt.Fprintf(out, "    Reason: %s\n", t.BlockedReason)
				}
			}
			fmt.Fprintln(out)
		}

		if status.NextTask != nil {
			fmt.Fprintln(out, "Next Suggested:")
			fmt.Fprintf(out, "  %s: %s\n", status.NextTask.ID, status.NextTask.Title)
			if len(status.NextTask.DependsOn) > 0 {
				fmt.Fprintf(out, "  Depends on: %s\n", strings.Join(status.NextTask.DependsOn, ", "))
			}
		} else if status.Pending > 0 {
			fmt.Fprintln(out, "All pending tasks have unmet dependencies.")
		} else if status.Pending == 0 && status.InProgress == 0 && status.Blocked == 0 {
			fmt.Fprintln(out, "All tasks are complete.")
		}

		return nil
	}
	return cmd
}
