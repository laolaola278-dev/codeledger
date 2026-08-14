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
	agent       string
	force       bool
	reason      string
	autoFiles   bool
	captureDiff bool
}

func newDoneCmd(deps Dependencies) *cobra.Command {
	o := &doneOptions{}
	cmd := newCommand("done <task-id>", "Mark a task as completed",
		`Mark a task as done with optional metadata.

Lease contract: completing a task with an active lease requires --agent
matching the lease owner. To complete a task leased by someone else (or in a
legacy pre-lease state), pass --force with an explicit --reason.

Flags:
  --files          Comma-separated list of modified files
  --test           Test command that was run
  --result         Test result: passed, failed, skipped, unknown
  --note           Completion note
  --agent          Agent completing the task (owner of the lease)
  --force          Break the lease to complete the task (requires --reason)
  --reason         Human-readable reason required with --force
  --auto-files     Automatically detect changed files from Git (default: false)
  --capture-diff   Capture full Git diff in evidence file (default: false)`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		taskID := args[0]
		return withProjectLock(deps, s, "done", o.agent, taskID, func() error {
			opts := service.CompleteOptions{
				Files:       o.files,
				Test:        o.test,
				Result:      o.result,
				Note:        o.note,
				AutoFiles:   o.autoFiles,
				CaptureDiff: o.captureDiff,
				Agent:       o.agent,
				Force:       o.force,
				Reason:      o.reason,
			}
			if err := service.CompleteTask(s, deps.Clock, taskID, opts); err != nil {
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
	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent completing the task (owner of the lease)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Break the lease to complete the task (requires --reason)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	cmd.Flags().BoolVar(&o.autoFiles, "auto-files", false, "Automatically detect changed files from Git")
	cmd.Flags().BoolVar(&o.captureDiff, "capture-diff", false, "Capture full Git diff in evidence file")
	return cmd
}
