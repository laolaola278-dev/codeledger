package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type nextOptions struct {
	role string
	json bool
}

func newNextCmd(deps Dependencies) *cobra.Command {
	o := &nextOptions{}
	cmd := newCommand("next", "Show the next available task to work on",
		`Find the next available task based on priority and dependencies.

Only tasks that are pending, have all dependencies completed, and are not
actively locked by another agent will be shown.

Flags:
  --role     Filter by role (optional, for future use)
  --json     Output in JSON format`)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		result, err := service.NextTask(s, o.role)
		if err != nil {
			return classifyErr("next failed", err)
		}

		out := cmd.OutOrStdout()
		if o.json {
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to marshal JSON")
			}
			fmt.Fprintln(out, string(data))
			return nil
		}

		if !result.Available {
			fmt.Fprintln(out, result.Message)
			return nil
		}

		t := result.Task
		fmt.Fprintln(out, "Next task:")
		fmt.Fprintf(out, "  %s %s\n", t.ID, t.Title)
		fmt.Fprintf(out, "  Priority: %s\n", t.Priority)
		if len(t.DependsOn) > 0 {
			fmt.Fprintf(out, "  Depends on: %s\n", strings.Join(t.DependsOn, ", "))
		}
		if t.Description != "" {
			fmt.Fprintf(out, "  Description: %s\n", t.Description)
		}

		return nil
	}

	cmd.Flags().StringVar(&o.role, "role", "", "Filter by role (optional)")
	cmd.Flags().BoolVar(&o.json, "json", false, "Output in JSON format")
	return cmd
}
