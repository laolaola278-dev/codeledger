package cmd

import (
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var listStatus string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tasks",
	Long: `List all tasks, optionally filtered by status.

Flags:
  --status   Filter by status: pending, in_progress, done, blocked`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		tasks, err := service.ListTasks(s, listStatus)
		if err != nil {
			return fmt.Errorf("list failed: %w", err)
		}

		if len(tasks) == 0 {
			if listStatus != "" {
				fmt.Printf("No tasks with status '%s'.\n", listStatus)
			} else {
				fmt.Println("No tasks. Use 'ctask add' to create one.")
			}
			return nil
		}

		// Header
		fmt.Printf("%-10s %-12s %-8s %s\n", "ID", "Status", "Priority", "Title")
		fmt.Println(strings.Repeat("-", 60))

		for _, t := range tasks {
			depStr := ""
			if len(t.DependsOn) > 0 {
				depStr = " [" + strings.Join(t.DependsOn, ",") + "]"
			}
			fmt.Printf("%-10s %-12s %-8s %s%s\n", t.ID, t.Status, t.Priority, t.Title, depStr)
		}

		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status: pending, in_progress, done, blocked")
	rootCmd.AddCommand(listCmd)
}
