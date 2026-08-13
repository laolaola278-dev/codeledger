package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ctask",
	Short: "CodeLedger - Project state ledger for AI coding agents",
	Long: `CodeLedger is a local-first project state ledger for AI coding agents.

It tracks project goals, tasks, decisions, modified files, test results,
and context summaries in a .ctask/ directory within your project.
Coding agents use it to resume work across sessions without losing context.`,
	SilenceUsage: true,
}

func Execute() error {
	return rootCmd.Execute()
}
