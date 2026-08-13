package cmd

import (
	"io"
	"os"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/spf13/cobra"
)

// Dependencies carries the injected I/O streams and working directory for a
// single command-tree execution. Each execution that needs isolation must
// provide its own writers and working directory; a fresh tree is built per
// execution so no flag or command state is shared.
type Dependencies struct {
	// Stdin is the command input stream.
	Stdin io.Reader
	// Stdout receives primary command output.
	Stdout io.Writer
	// Stderr receives diagnostics, warnings, and text-mode errors.
	Stderr io.Writer
	// WorkDir is the directory commands operate in. Empty means the process
	// current directory.
	WorkDir string
}

// NewDependencies returns the default dependencies bound to the process
// standard streams and current directory.
func NewDependencies() Dependencies {
	return Dependencies{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// newCommand creates a command wired with the process contract: Cobra's own
// error/usage printing is silenced (a single renderer in Execute owns
// rendering), and flag-parse failures are classified as USAGE_ERROR.
func newCommand(use, short, long string) *cobra.Command {
	c := &cobra.Command{
		Use:           use,
		Short:         short,
		Long:          long,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return clierr.Wrap(clierr.KindUsage, err, "invalid flag")
	})
	return c
}

// NewRoot builds a completely fresh command tree. Every call returns a new
// tree with its own command objects and flag storage, so multiple roots can
// be constructed and executed concurrently in the same process without
// sharing state.
func NewRoot(deps Dependencies) *cobra.Command {
	root := newCommand("ctask", "CodeLedger - Project state ledger for AI coding agents",
		`CodeLedger is a local-first project state ledger for AI coding agents.

It tracks project goals, tasks, decisions, modified files, test results,
and context summaries in a .ctask/ directory within your project.
Coding agents use it to resume work across sessions without losing context.`)
	root.SetIn(deps.Stdin)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)

	root.AddCommand(
		newAddCmd(deps),
		newBlockCmd(deps),
		newChangedCmd(deps),
		newCheckCmd(deps),
		newClaimCmd(deps),
		newContextCmd(deps),
		newDiffCmd(deps),
		newDoneCmd(deps),
		newEvidenceCmd(deps),
		newFinishCmd(deps),
		newHeartbeatCmd(deps),
		newInitCmd(deps),
		newListCmd(deps),
		newLocksCmd(deps),
		newNextCmd(deps),
		newNoteCmd(deps),
		newPlanCmd(deps),
		newReleaseCmd(deps),
		newReportCmd(deps),
		newStartCmd(deps),
		newStatusCmd(deps),
	)

	return root
}
