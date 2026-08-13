package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/contextgen"
	"github.com/spf13/cobra"
)

func newContextCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("context", "Generate context summary for AI coding agents",
		`Generate a Markdown context summary from the current project state.

The output is written to .ctask/context.md and also printed to stdout.
This context is designed to be fed to an AI coding agent at the start
of a new session so it can resume work without losing information.`)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		ctx, err := contextgen.GenerateContext(s)
		if err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "context generation failed")
		}

		// Write to .ctask/context.md
		if err := s.WriteContext(ctx); err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "failed to write context.md")
		}

		// Print to stdout
		fmt.Fprintln(cmd.OutOrStdout(), ctx)

		return nil
	}
	return cmd
}
