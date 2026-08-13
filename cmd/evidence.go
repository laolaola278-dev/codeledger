package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type evidenceAddOptions struct {
	typ     string
	content string
	file    string
}

func newEvidenceCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("evidence [task-id]", "Manage task evidence",
		`Manage evidence recorded for a task.

Without a subcommand, shows the evidence for the given task (equivalent to "show").

Subcommands:
  add    Append evidence to a task's evidence file
  list   List all evidence paths for a task
  show   Show the Markdown evidence content for a task`)
	cmd.Args = cobra.ArbitraryArgs
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return showEvidence(cmd, deps, args[0])
	}

	addOpts := &evidenceAddOptions{}
	addCmd := newCommand("add <task-id>", "Add evidence to a task",
		`Append evidence to a task's evidence file (.ctask/evidence/<task-id>.md).

Provide inline content with --content, or reference a file with --file.
Use --type to label the evidence (e.g. test, review, manual).`)
	addCmd.Args = exactArgs(1)
	addCmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}
		taskID := args[0]
		content := addOpts.content
		if addOpts.file != "" {
			data, err := os.ReadFile(addOpts.file)
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to read file")
			}
			content = string(data)
		}
		if content == "" {
			return clierr.New(clierr.KindUsage, "provide evidence via --content or --file")
		}
		et := addOpts.typ
		if et == "" {
			et = "manual"
		}
		return withProjectLock(deps, s, "evidence add", "", taskID, func() error {
			if err := service.AddEvidence(s, taskID, et, content); err != nil {
				return classifyErr("evidence add failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Evidence added to task %s.\n", taskID)
			return nil
		})
	}
	addCmd.Flags().StringVar(&addOpts.typ, "type", "manual", "Evidence type (e.g. test, review, manual)")
	addCmd.Flags().StringVar(&addOpts.content, "content", "", "Inline evidence content")
	addCmd.Flags().StringVar(&addOpts.file, "file", "", "Read evidence content from a file")

	listCmd := newCommand("list <task-id>", "List evidence paths for a task", "")
	listCmd.Args = exactArgs(1)
	listCmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}
		task, err := service.GetTaskByID(s, args[0])
		if err != nil {
			return classifyErr("", err)
		}
		out := cmd.OutOrStdout()
		if len(task.Evidence) == 0 {
			fmt.Fprintln(out, "No evidence recorded.")
			return nil
		}
		fmt.Fprintf(out, "Evidence for %s (%d):\n", task.ID, len(task.Evidence))
		for _, e := range task.Evidence {
			fmt.Fprintln(out, "  "+e)
		}
		return nil
	}

	showCmd := newCommand("show <task-id>", "Show Markdown evidence content for a task", "")
	showCmd.Args = exactArgs(1)
	showCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return showEvidence(cmd, deps, args[0])
	}

	cmd.AddCommand(addCmd, listCmd, showCmd)
	return cmd
}

// showEvidence reads and displays the .md evidence file for a task.
func showEvidence(cmd *cobra.Command, deps Dependencies, taskID string) error {
	s := newStore(deps)
	if err := requireInit(s); err != nil {
		return err
	}
	// Verify the task exists
	if _, err := service.GetTaskByID(s, taskID); err != nil {
		return classifyErr("", err)
	}
	out := cmd.OutOrStdout()
	evidencePath := s.EvidencePath(taskID)
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		if os.IsNotExist(err) {
			return clierr.New(clierr.KindNotFound, "no evidence file found for task %s", taskID)
		}
		return clierr.Wrap(clierr.KindOperation, err, "failed to read evidence")
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		fmt.Fprintln(out, "Evidence file is empty.")
		return nil
	}
	fmt.Fprint(out, string(data))
	return nil
}
