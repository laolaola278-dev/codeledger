package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize .ctask directory in the current project",
	Long: `Create the .ctask directory with default files:
  project.yaml    - Project metadata and agent policy
  tasks.yaml      - Empty task list
  decisions.md    - Decision log template
  context.md      - Context summary placeholder
  events.jsonl    - Event log (initially empty)
  reports/        - Reports directory`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := service.InitProject(s); err != nil {
			return fmt.Errorf("init failed: %w", err)
		}
		fmt.Println("Initialized .ctask directory.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
