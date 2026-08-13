package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

func newInitCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("init", "Initialize .ctask directory in the current project",
		`Create the .ctask directory with default files:
  project.yaml    - Project metadata and agent policy
  tasks.yaml      - Empty task list
  decisions.md    - Decision log template
  context.md      - Context summary placeholder
  events.jsonl    - Event log (initially empty)
  reports/        - Reports directory`)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := service.InitProject(s); err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "init failed")
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Initialized .ctask directory.")
		return nil
	}
	return cmd
}
