package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/codeledger/codeledger/internal/git"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	diffStat     bool
	diffNameOnly bool
	diffCached   bool
	diffJSON     bool
)

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show Git diff for the working tree",
	Long: `Show the Git diff for the working tree.

By default, shows the full working-tree diff (unstaged changes).
Use --cached to show only staged changes.
Use --stat for a diffstat summary.
Use --name-only for just the file names.

This command requires a Git repository.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		gitDir := filepath.Dir(s.BasePath)
		if !git.IsGitRepo(gitDir) {
			return fmt.Errorf("not a git repository")
		}

		switch {
		case diffStat:
			out, err := git.DiffStat(gitDir)
			if err != nil {
				return fmt.Errorf("failed to get diffstat: %w", err)
			}
			if out == "" {
				fmt.Println("No diff.")
				return nil
			}
			fmt.Print(out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				fmt.Println()
			}
		case diffNameOnly:
			files, err := git.DiffNameOnly(gitDir, diffCached)
			if err != nil {
				return fmt.Errorf("failed to get diff file names: %w", err)
			}
			if len(files) == 0 {
				fmt.Println("No diff.")
				return nil
			}
			for _, f := range files {
				fmt.Println(f)
			}
		default:
			var out string
			var err error
			if diffCached {
				out, err = git.Diff(gitDir, true)
			} else {
				out, err = git.FullDiff(gitDir)
			}
			if err != nil {
				return fmt.Errorf("failed to get diff: %w", err)
			}
			if out == "" {
				fmt.Println("No diff.")
				return nil
			}
			fmt.Print(out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				fmt.Println()
			}
		}

		evt := model.NewEvent(model.EventDiffListed, "", "", "diff listed")
		if err := s.AppendEvent(evt); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		return nil
	},
}

func init() {
	diffCmd.Flags().BoolVar(&diffStat, "stat", false, "Show diffstat summary")
	diffCmd.Flags().BoolVar(&diffNameOnly, "name-only", false, "Show only file names")
	diffCmd.Flags().BoolVar(&diffCached, "cached", false, "Show staged (cached) diff only")
	rootCmd.AddCommand(diffCmd)
}
