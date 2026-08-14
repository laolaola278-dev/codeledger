package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type checkOptions struct {
	json    bool
	verbose bool
	strict  bool
}

func newCheckCmd(deps Dependencies) *cobra.Command {
	o := &checkOptions{}
	cmd := newCommand("check", "Check .ctask project consistency",
		`Run a consistency check on the .ctask project state.

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

Exit code 0 if no failures (or no warnings in --strict mode), 1 otherwise.`)
	cmd.Args = noArgs()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		result := service.RunCheck(s, deps.Clock)

		// Log event
		var evtType string
		if result.HasFailures() {
			evtType = model.EventCheckFailed
		} else {
			evtType = model.EventCheckPassed
		}
		evt := model.NewEvent(evtType, "", "", fmt.Sprintf("%d checks, %d failures", len(result.Checks), countFailures(result)))
		_ = s.AppendEvent(evt)

		failing := result.HasFailures() || (o.strict && result.HasWarnings())

		if o.json {
			if failing {
				// The JSON error envelope replaces the result document so
				// stdout always contains exactly one JSON document.
				return checkFailedError(result, o.strict)
			}
			out, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to marshal JSON")
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		}

		printCheckResult(cmd.OutOrStdout(), result, o.verbose)
		if failing {
			return checkFailedError(result, o.strict)
		}
		return nil
	}

	cmd.Flags().BoolVar(&o.json, "json", false, "Output check results as JSON")
	cmd.Flags().BoolVar(&o.verbose, "verbose", false, "Show all checks including passing ones")
	cmd.Flags().BoolVar(&o.strict, "strict", false, "Treat warnings as failures (exit code 1 on any warn or fail)")
	return cmd
}

// checkFailedError builds the typed CHECK_FAILED error for a consistency
// check that found failures (or warnings in strict mode).
func checkFailedError(result *service.CheckResult, strict bool) error {
	return clierr.New(clierr.KindCheckFailed,
		"consistency check failed: %d failure(s), %d warning(s) (strict=%v)",
		countFailures(result), countWarnings(result), strict)
}

// printCheckResult renders the consistency check report to w.
func printCheckResult(w io.Writer, r *service.CheckResult, verbose bool) {
	fmt.Fprintln(w, "CodeLedger consistency check")
	fmt.Fprintln(w)

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
			fmt.Fprintf(w, "  %-6s %s", icon, c.Name)
			if c.Message != "" {
				fmt.Fprintf(w, " - %s", c.Message)
			}
			fmt.Fprintln(w)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %d pass, %d info, %d warn, %d fail (total %d)\n", pass, info, warn, fail, len(r.Checks))
	if fail > 0 {
		fmt.Fprintln(w, "Result: FAILED")
	} else {
		fmt.Fprintln(w, "Result: OK")
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

func countWarnings(r *service.CheckResult) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == service.CheckWarn {
			n++
		}
	}
	return n
}
