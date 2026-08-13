package cmd

import (
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show project status overview",
	Long: `Display a summary of the project's current status:
  - Total / pending / in_progress / done / blocked task counts
  - Current in-progress task
  - Blocked tasks with reasons
  - Next suggested task to work on`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		status, err := service.GetStatus(s)
		if err != nil {
			return fmt.Errorf("status failed: %w", err)
		}

		fmt.Printf("Project: %s\n\n", status.ProjectName)
		fmt.Println("Progress:")
		fmt.Printf("  Done:       %d\n", status.Done)
		fmt.Printf("  In Progress: %d\n", status.InProgress)
		fmt.Printf("  Pending:    %d\n", status.Pending)
		fmt.Printf("  Blocked:    %d\n", status.Blocked)
		fmt.Println()

		if status.CurrentTask != nil {
			fmt.Println("Current Task:")
			fmt.Printf("  %s: %s\n", status.CurrentTask.ID, status.CurrentTask.Title)
			fmt.Println()
		}

		if len(status.BlockedTasks) > 0 {
			fmt.Println("Blocked Tasks:")
			for _, t := range status.BlockedTasks {
				fmt.Printf("  %s: %s\n", t.ID, t.Title)
				if t.BlockedReason != "" {
					fmt.Printf("    Reason: %s\n", t.BlockedReason)
				}
			}
			fmt.Println()
		}

		if status.NextTask != nil {
			fmt.Println("Next Suggested:")
			fmt.Printf("  %s: %s\n", status.NextTask.ID, status.NextTask.Title)
			if len(status.NextTask.DependsOn) > 0 {
				fmt.Printf("  Depends on: %s\n", strings.Join(status.NextTask.DependsOn, ", "))
			}
		} else if status.Pending > 0 {
			fmt.Println("All pending tasks have unmet dependencies.")
		} else if status.Pending == 0 && status.InProgress == 0 && status.Blocked == 0 {
			fmt.Println("All tasks are complete.")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
