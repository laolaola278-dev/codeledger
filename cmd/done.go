package cmd

import (
	"fmt"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/spf13/cobra"
)

type doneOptions struct {
	files       string
	test        string
	result      string
	note        string
	autoFiles   bool
	captureDiff bool
}

func newDoneCmd(deps Dependencies) *cobra.Command {
	o := &doneOptions{}
	cmd := newCommand("done <task-id>", "Mark a task as completed",
		`Mark a task as done with optional metadata.

Flags:
  --files          Comma-separated list of modified files
  --test           Test command that was run
  --result         Test result: passed, failed, skipped, unknown
  --note           Completion note
  --auto-files     Automatically detect changed files from Git (default: false)
  --capture-diff   Capture full Git diff in evidence file (default: false)`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "done", "", taskID, func() error {
			if err := service.CompleteTask(s, taskID, o.files, o.test, o.result, o.note, o.autoFiles, o.captureDiff); err != nil {
				return classifyErr("done failed", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Completed task %s.\n", taskID)
			return nil
		})
	}

	cmd.Flags().StringVar(&o.files, "files", "", "Comma-separated list of modified files")
	cmd.Flags().StringVar(&o.test, "test", "", "Test command that was run")
	cmd.Flags().StringVar(&o.result, "result", "", "Test result: passed, failed, skipped, unknown")
	cmd.Flags().StringVar(&o.note, "note", "", "Completion note")
	cmd.Flags().BoolVar(&o.autoFiles, "auto-files", false, "Automatically detect changed files from Git")
	cmd.Flags().BoolVar(&o.captureDiff, "capture-diff", false, "Capture full Git diff in evidence file")
	return cmd
}
