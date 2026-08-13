package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/spf13/cobra"
)

// jsonFlagName is the flag name used by every command that supports
// machine-readable JSON output. renderError checks the executed command for
// this flag to decide between a JSON error envelope and plain text.
const jsonFlagName = "json"

// Execute builds a fresh command tree, runs it with args, renders any error
// exactly once, and returns the stable process exit code (see clierr.ExitCode).
// main() only needs to os.Exit with the returned value, so the error is never
// printed twice and never silently swallowed.
func Execute(ctx context.Context, deps Dependencies, args []string) int {
	root := NewRoot(deps)
	root.SetArgs(args)

	// Classify unknown top-level commands as usage errors before Cobra runs.
	if _, _, err := root.Find(args); err != nil {
		wrapped := clierr.Wrap(clierr.KindUsage, err, "")
		renderError(deps, root, wrapped)
		return clierr.ExitCode(wrapped)
	}

	executed, err := root.ExecuteContextC(ctx)
	if err != nil {
		renderError(deps, executed, err)
		return clierr.ExitCode(err)
	}
	return clierr.ExitOK
}

// errorEnvelope is the machine-readable error output for --json commands.
type errorEnvelope struct {
	OK    bool        `json:"ok"`
	Error errorDetail `json:"error"`
}

// errorDetail carries the stable machine error code and a human message.
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// renderError prints err exactly once. Commands run with --json get a JSON
// error envelope on stdout; everything else gets a single line on stderr.
func renderError(deps Dependencies, executed *cobra.Command, err error) {
	if err == nil {
		return
	}
	if jsonRequested(executed) {
		env := errorEnvelope{
			OK: false,
			Error: errorDetail{
				Code:    string(clierr.KindOf(err)),
				Message: err.Error(),
			},
		}
		if data, merr := json.MarshalIndent(env, "", "  "); merr == nil {
			fmt.Fprintln(deps.Stdout, string(data))
			return
		}
	}
	fmt.Fprintf(deps.Stderr, "Error: %v\n", err)
}

// jsonRequested reports whether the executed command was run with --json.
func jsonRequested(executed *cobra.Command) bool {
	if executed == nil {
		return false
	}
	f := executed.Flags().Lookup(jsonFlagName)
	if f == nil {
		return false
	}
	v, err := executed.Flags().GetBool(jsonFlagName)
	return err == nil && v
}
