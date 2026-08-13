package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/git"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/spf13/cobra"
)

type diffOptions struct {
	stat     bool
	nameOnly bool
	cached   bool
}

func newDiffCmd(deps Dependencies) *cobra.Command {
	o := &diffOptions{}
	cmd := newCommand("diff", "Show Git diff for the working tree",
		`Show the Git diff for the working tree.

By default, shows the full working-tree diff (unstaged changes).
Use --cached to show only staged changes.
Use --stat for a diffstat summary.
Use --name-only for just the file names.

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

		stdout := cmd.OutOrStdout()
		switch {
		case o.stat:
			out, err := git.DiffStat(gitDir)
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to get diffstat")
			}
			if out == "" {
				fmt.Fprintln(stdout, "No diff.")
				return nil
			}
			fmt.Fprint(stdout, out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				fmt.Fprintln(stdout)
			}
		case o.nameOnly:
			files, err := git.DiffNameOnly(gitDir, o.cached)
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to get diff file names")
			}
			if len(files) == 0 {
				fmt.Fprintln(stdout, "No diff.")
				return nil
			}
			for _, f := range files {
				fmt.Fprintln(stdout, f)
			}
		default:
			var out string
			var err error
			if o.cached {
				out, err = git.Diff(gitDir, true)
			} else {
				out, err = git.FullDiff(gitDir)
			}
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to get diff")
			}
			if out == "" {
				fmt.Fprintln(stdout, "No diff.")
				return nil
			}
			fmt.Fprint(stdout, out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				fmt.Fprintln(stdout)
			}
		}

		evt := model.NewEvent(model.EventDiffListed, "", "", "diff listed")
		if err := s.AppendEvent(evt); err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "failed to log event")
		}

		return nil
	}

	cmd.Flags().BoolVar(&o.stat, "stat", false, "Show diffstat summary")
	cmd.Flags().BoolVar(&o.nameOnly, "name-only", false, "Show only file names")
	cmd.Flags().BoolVar(&o.cached, "cached", false, "Show staged (cached) diff only")
	return cmd
}
