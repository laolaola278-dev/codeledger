package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/git"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/spf13/cobra"
)

type changedOptions struct {
	json bool
}

func newChangedCmd(deps Dependencies) *cobra.Command {
	o := &changedOptions{}
	cmd := newCommand("changed", "List changed files in the Git working tree",
		`List files that have been modified, added, or deleted in the Git working tree
(including staged and unstaged changes, and untracked files).

Use --json for machine-readable output.

This command requires a Git repository.`)
	cmd.Args = noArgs()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		gitDir := filepath.Dir(s.BasePath)
		if !git.IsGitRepo(gitDir) {
			return clierr.Wrap(clierr.KindOperation, fmt.Errorf("not a git repository"), "")
		}

		files, err := git.ChangedFiles(gitDir)
		if err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "failed to list changed files")
		}

		stdout := cmd.OutOrStdout()
		if o.json {
			out, err := json.MarshalIndent(files, "", "  ")
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to marshal JSON")
			}
			fmt.Fprintln(stdout, string(out))
			return nil
		}

		if len(files) == 0 {
			fmt.Fprintln(stdout, "No changed files.")
			return nil
		}

		fmt.Fprintf(stdout, "Changed files (%d):\n", len(files))
		for _, f := range files {
			fmt.Fprintln(stdout, "  "+f)
		}

		evt := model.NewEvent(model.EventChangedListed, "", "", fmt.Sprintf("listed %d changed files", len(files)))
		if err := s.AppendEvent(evt); err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "failed to log event")
		}

		return nil
	}

	cmd.Flags().BoolVar(&o.json, "json", false, "Output changed files as JSON array")
	return cmd
}
