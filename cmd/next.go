package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	nextRole string
	nextJSON bool
)

var nextCmd = &cobra.Command{
	Use:   "next",
	Short: "Show the next available task to work on",
	Long: `Find the next available task based on priority and dependencies.

Only tasks that are pending, have all dependencies completed, and are not
actively locked by another agent will be shown.

Flags:
  --role     Filter by role (optional, for future use)
  --json     Output in JSON format`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		result, err := service.NextTask(s, nextRole)
		if err != nil {
			return fmt.Errorf("next failed: %w", err)
		}

		if nextJSON {
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal JSON: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		if !result.Available {
			fmt.Println(result.Message)
			return nil
		}

		t := result.Task
		fmt.Println("Next task:")
		fmt.Printf("  %s %s\n", t.ID, t.Title)
		fmt.Printf("  Priority: %s\n", t.Priority)
		if len(t.DependsOn) > 0 {
			fmt.Printf("  Depends on: %s\n", strings.Join(t.DependsOn, ", "))
		}
		if t.Description != "" {
			fmt.Printf("  Description: %s\n", t.Description)
		}

		return nil
	},
}

func init() {
	nextCmd.Flags().StringVar(&nextRole, "role", "", "Filter by role (optional)")
	nextCmd.Flags().BoolVar(&nextJSON, "json", false, "Output in JSON format")
	rootCmd.AddCommand(nextCmd)
}
