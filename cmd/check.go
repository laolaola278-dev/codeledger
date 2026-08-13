package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	checkJSON    bool
	checkVerbose bool
	checkStrict  bool
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check .ctask project consistency",
	Long: `Run a consistency check on the .ctask project state.

Validates:
  - project.yaml is readable
  - tasks.yaml is readable, IDs unique, statuses/priorities valid
  - done tasks have completed_at timestamps
  - task dependencies reference existing tasks
  - evidence files referenced in tasks exist on disk
  - locks.yaml is readable, locks reference existing tasks, no expired locks
  - events.jsonl is readable

Use --json for machine-readable output.
Use --verbose for full detail on every check.
Use --strict to treat warnings as failures (exit code 1 on any warn or fail).

Exit code 0 if no failures (or no warnings in --strict mode), 1 otherwise.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		result := service.RunCheck(s)

		// Log event
		var evtType string
		if result.HasFailures() {
			evtType = model.EventCheckFailed
		} else {
			evtType = model.EventCheckPassed
		}
		evt := model.NewEvent(evtType, "", "", fmt.Sprintf("%d checks, %d failures", len(result.Checks), countFailures(result)))
		_ = s.AppendEvent(evt)

		if checkJSON {
			out, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal JSON: %w", err)
			}
			fmt.Println(string(out))
		} else {
			printCheckResult(result, checkVerbose)
		}

		shouldExitNonZero := result.HasFailures()
		if checkStrict && result.HasWarnings() {
			shouldExitNonZero = true
		}
		if shouldExitNonZero {
			os.Exit(1)
		}
		return nil
	},
}

func printCheckResult(r *service.CheckResult, verbose bool) {
	fmt.Println("CodeLedger consistency check")
	fmt.Println()

	pass, fail, warn, info := 0, 0, 0, 0
	for _, c := range r.Checks {
		switch c.Status {
		case service.CheckPass:
			pass++
		case service.CheckFail:
			fail++
		case service.CheckWarn:
			warn++
		case service.CheckInfo:
			info++
		}
	}

	for _, c := range r.Checks {
		icon := "[PASS]"
		switch c.Status {
		case service.CheckFail:
			icon = "[FAIL]"
		case service.CheckWarn:
			icon = "[WARN]"
		case service.CheckInfo:
			icon = "[INFO]"
		}
		if verbose || c.Status == service.CheckFail || c.Status == service.CheckWarn || c.Status == service.CheckInfo {
			fmt.Printf("  %-6s %s", icon, c.Name)
			if c.Message != "" {
				fmt.Printf(" - %s", c.Message)
			}
			fmt.Println()
		}
	}

	fmt.Println()
	fmt.Printf("Summary: %d pass, %d info, %d warn, %d fail (total %d)\n", pass, info, warn, fail, len(r.Checks))
	if fail > 0 {
		fmt.Println("Result: FAILED")
	} else {
		fmt.Println("Result: OK")
	}
}

func countFailures(r *service.CheckResult) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == service.CheckFail {
			n++
		}
	}
	return n
}

func init() {
	checkCmd.Flags().BoolVar(&checkJSON, "json", false, "Output check results as JSON")
	checkCmd.Flags().BoolVar(&checkVerbose, "verbose", false, "Show all checks including passing ones")
	checkCmd.Flags().BoolVar(&checkStrict, "strict", false, "Treat warnings as failures (exit code 1 on any warn or fail)")
	rootCmd.AddCommand(checkCmd)
}
