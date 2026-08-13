package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/contextgen"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Generate context summary for AI coding agents",
	Long: `Generate a Markdown context summary from the current project state.

The output is written to .ctask/context.md and also printed to stdout.
This context is designed to be fed to an AI coding agent at the start
of a new session so it can resume work without losing information.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		ctx, err := contextgen.GenerateContext(s)
		if err != nil {
			return fmt.Errorf("context generation failed: %w", err)
		}

		// Write to .ctask/context.md
		if err := s.WriteContext(ctx); err != nil {
			return fmt.Errorf("failed to write context.md: %w", err)
		}

		// Print to stdout
		fmt.Println(ctx)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(contextCmd)
}
