package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	evidenceAddType    string
	evidenceAddContent string
	evidenceAddFile    string
)

var evidenceCmd = &cobra.Command{
	Use:   "evidence [task-id]",
	Short: "Manage task evidence",
	Long: `Manage evidence recorded for a task.

Without a subcommand, shows the evidence for the given task (equivalent to "show").

Subcommands:
  add    Append evidence to a task's evidence file
  list   List all evidence paths for a task
  show   Show the Markdown evidence content for a task`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return showEvidence(args[0])
	},
}

var evidenceAddCmd = &cobra.Command{
	Use:   "add <task-id>",
	Short: "Add evidence to a task",
	Long: `Append evidence to a task's evidence file (.ctask/evidence/<task-id>.md).

Provide inline content with --content, or reference a file with --file.
Use --type to label the evidence (e.g. test, review, manual).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}
		taskID := args[0]
		content := evidenceAddContent
		if evidenceAddFile != "" {
			data, err := os.ReadFile(evidenceAddFile)
			if err != nil {
				return fmt.Errorf("failed to read file: %w", err)
			}
			content = string(data)
		}
		if content == "" {
			return fmt.Errorf("provide evidence via --content or --file")
		}
		et := evidenceAddType
		if et == "" {
			et = "manual"
		}
		return withProjectLock(s, "evidence add", "", taskID, func() error {
			if err := service.AddEvidence(s, taskID, et, content); err != nil {
				return fmt.Errorf("evidence add failed: %w", err)
			}
			fmt.Printf("Evidence added to task %s.\n", taskID)
			return nil
		})
	},
}

var evidenceListCmd = &cobra.Command{
	Use:   "list <task-id>",
	Short: "List evidence paths for a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}
		task, err := service.GetTaskByID(s, args[0])
		if err != nil {
			return err
		}
		if len(task.Evidence) == 0 {
			fmt.Println("No evidence recorded.")
			return nil
		}
		fmt.Printf("Evidence for %s (%d):\n", task.ID, len(task.Evidence))
		for _, e := range task.Evidence {
			fmt.Println("  " + e)
		}
		return nil
	},
}

var evidenceShowCmd = &cobra.Command{
	Use:   "show <task-id>",
	Short: "Show Markdown evidence content for a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return showEvidence(args[0])
	},
}

// showEvidence reads and displays the .md evidence file for a task.
func showEvidence(taskID string) error {
	s := store.NewStore(".")
	if err := s.RequireInit(); err != nil {
		return err
	}
	// Verify the task exists
	if _, err := service.GetTaskByID(s, taskID); err != nil {
		return err
	}
	evidencePath := s.EvidencePath(taskID)
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no evidence file found for task %s", taskID)
		}
		return fmt.Errorf("failed to read evidence: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		fmt.Println("Evidence file is empty.")
		return nil
	}
	fmt.Print(string(data))
	return nil
}

func init() {
	evidenceAddCmd.Flags().StringVar(&evidenceAddType, "type", "manual", "Evidence type (e.g. test, review, manual)")
	evidenceAddCmd.Flags().StringVar(&evidenceAddContent, "content", "", "Inline evidence content")
	evidenceAddCmd.Flags().StringVar(&evidenceAddFile, "file", "", "Read evidence content from a file")

	evidenceCmd.AddCommand(evidenceAddCmd)
	evidenceCmd.AddCommand(evidenceListCmd)
	evidenceCmd.AddCommand(evidenceShowCmd)
	rootCmd.AddCommand(evidenceCmd)
}
