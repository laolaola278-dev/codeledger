package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/codeledger/codeledger/internal/git"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var changedJSON bool

var changedCmd = &cobra.Command{
	Use:   "changed",
	Short: "List changed files in the Git working tree",
	Long: `List files that have been modified, added, or deleted in the Git working tree
(including staged and unstaged changes, and untracked files).

Use --json for machine-readable output.

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

		files, err := git.ChangedFiles(gitDir)
		if err != nil {
			return fmt.Errorf("failed to list changed files: %w", err)
		}

		if changedJSON {
			out, err := json.MarshalIndent(files, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal JSON: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		if len(files) == 0 {
			fmt.Println("No changed files.")
			return nil
		}

		fmt.Printf("Changed files (%d):\n", len(files))
		for _, f := range files {
			fmt.Println("  " + f)
		}

		evt := model.NewEvent(model.EventChangedListed, "", "", fmt.Sprintf("listed %d changed files", len(files)))
		if err := s.AppendEvent(evt); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		return nil
	},
}

func init() {
	changedCmd.Flags().BoolVar(&changedJSON, "json", false, "Output changed files as JSON array")
	rootCmd.AddCommand(changedCmd)
}
