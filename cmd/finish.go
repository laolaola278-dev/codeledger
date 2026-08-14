package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/contextgen"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/reportgen"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

type finishOptions struct {
	task        string
	files       string
	test        string
	result      string
	note        string
	agent       string
	leaseID     string
	force       bool
	reason      string
	autoFiles   bool
	captureDiff bool
	skipReport  bool
	skipContext bool
	strict      bool
	json        bool
}

// finishJSONOutput is the JSON structure for --json output.
type finishJSONOutput struct {
	Check          *service.CheckResult `json:"check,omitempty"`
	TaskCompleted  string               `json:"task_completed"`
	ContextUpdated bool                 `json:"context_updated"`
	ReportSaved    string               `json:"report_saved,omitempty"`
	NextTask       string               `json:"next_task,omitempty"`
	Errors         []string             `json:"errors,omitempty"`
}

func newFinishCmd(deps Dependencies) *cobra.Command {
	o := &finishOptions{}
	cmd := newCommand("finish", "End an agent session: check, complete task, generate context and report",
		`Run a session finish sequence.

Steps:
  1. Run consistency check (ctask check)
  2. Optionally complete a task (--task TASK-001 with --files/--test/--result/--note)
  3. Generate .ctask/context.md (unless --skip-context)
  4. Generate .ctask/reports/YYYY-MM-DD-report.md (unless --skip-report)
  5. Print next suggested task
  6. Log a session.finished event

If the check finds failures, finish still continues but warns the user.
Use --strict to make warnings in the check step cause a non-zero exit code.
Use --agent to record which agent is finishing the session.
Use --json for machine-readable JSON output.

Flags:
  --task              Task ID to complete (optional)
  --agent             Agent name performing the finish (optional)
  --files             Comma-separated modified files (with --task)
  --test              Test command that was run (with --task)
  --result            Test result: passed, failed, skipped, unknown (with --task)
  --note              Completion note (with --task)
  --auto-files        Auto-detect changed files from Git (with --task)
  --capture-diff      Capture full Git diff (with --task)
  --skip-context      Do not regenerate context.md
  --skip-report       Do not generate a report file
  --strict            Treat check warnings as failures (exit code 1)
  --json              Output results as JSON`)
	cmd.Args = noArgs()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		// The project lock is released (deferred) before this function's
		// error is acted upon, so a strict-mode failure propagates as a
		// typed error instead of os.Exit.
		return withProjectLock(deps, s, "finish", o.agent, o.task, func() error {
			return runFinish(cmd, deps, s, o)
		})
	}

	cmd.Flags().StringVar(&o.task, "task", "", "Task ID to complete before finishing")
	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name performing the finish")
	cmd.Flags().StringVar(&o.leaseID, "lease-id", "", "Lease ID (required to complete a leased task)")
	cmd.Flags().BoolVar(&o.force, "force", false, "Break the lease to complete the task (requires --reason and --agent)")
	cmd.Flags().StringVar(&o.reason, "reason", "", "Human-readable reason required with --force")
	cmd.Flags().StringVar(&o.files, "files", "", "Comma-separated modified files (with --task)")
	cmd.Flags().StringVar(&o.test, "test", "", "Test command that was run (with --task)")
	cmd.Flags().StringVar(&o.result, "result", "", "Test result: passed, failed, skipped, unknown (with --task)")
	cmd.Flags().StringVar(&o.note, "note", "", "Completion note (with --task)")
	cmd.Flags().BoolVar(&o.autoFiles, "auto-files", false, "Auto-detect changed files from Git (with --task)")
	cmd.Flags().BoolVar(&o.captureDiff, "capture-diff", false, "Capture full Git diff (with --task)")
	cmd.Flags().BoolVar(&o.skipContext, "skip-context", false, "Do not regenerate context.md")
	cmd.Flags().BoolVar(&o.skipReport, "skip-report", false, "Do not generate a report file")
	cmd.Flags().BoolVar(&o.strict, "strict", false, "Treat check warnings as failures (exit code 1)")
	cmd.Flags().BoolVar(&o.json, "json", false, "Output results as JSON")
	return cmd
}

// runFinish executes the finish sequence while the project mutation lock is
// held. All failures are returned as typed errors; no os.Exit is used, so
// deferred cleanup (including project lock release) always runs first.
func runFinish(cmd *cobra.Command, deps Dependencies, s *store.Store, o *finishOptions) error {
	out := cmd.OutOrStdout()
	var jsonOut finishJSONOutput
	var jsonErrors []string

	if !o.json {
		fmt.Fprintln(out, "=== CodeLedger Session Finish ===")
		fmt.Fprintln(out)
	}

	// Step 1: Check
	if !o.json {
		fmt.Fprintln(out, "[1/5] Running consistency check...")
	}
	result := service.RunCheck(s, deps.Clock)
	jsonOut.Check = result

	hasCheckIssues := result.HasFailures()
	if o.strict && result.HasWarnings() {
		hasCheckIssues = true
	}

	if !o.json {
		printCheckResult(out, result, false)
	}

	var evtType string
	if result.HasFailures() {
		evtType = model.EventCheckFailed
		if !o.json {
			fmt.Fprintln(out, "WARNING: check found failures. Continuing finish sequence.")
		}
	} else {
		evtType = model.EventCheckPassed
	}
	checkEvt := model.NewEvent(evtType, "", "", fmt.Sprintf("check: %d checks", len(result.Checks)))
	if o.agent != "" {
		checkEvt.Agent = o.agent
	}
	_ = s.AppendEvent(checkEvt)
	if !o.json {
		fmt.Fprintln(out)
	}

	// Step 2: Optionally complete a task
	if o.task != "" {
		if o.result == "" {
			if !o.json {
				fmt.Fprintf(out, "[2/5] Task %s not completed: --result is required to complete a task (use --result passed|failed|skipped|unknown).\n", o.task)
			}
		} else {
			if !o.json {
				fmt.Fprintf(out, "[2/5] Completing task %s...\n", o.task)
			}
			opts := service.CompleteOptions{
				Files:       o.files,
				Test:        o.test,
				Result:      o.result,
				Note:        o.note,
				AutoFiles:   o.autoFiles,
				CaptureDiff: o.captureDiff,
				Agent:       o.agent,
				LeaseID:     o.leaseID,
				Force:       o.force,
				Reason:      o.reason,
			}
			if err := service.CompleteTask(s, deps.Clock, o.task, opts); err != nil {
				if o.json {
					jsonErrors = append(jsonErrors, fmt.Sprintf("failed to complete task %s: %v", o.task, err))
				} else {
					return classifyErr(fmt.Sprintf("finish: failed to complete task %s", o.task), err)
				}
			} else {
				jsonOut.TaskCompleted = o.task
				if !o.json {
					fmt.Fprintf(out, "  Completed task %s.\n", o.task)
				}
			}
		}
	} else {
		if !o.json {
			fmt.Fprintln(out, "[2/5] No task to complete (--task not specified).")
		}
	}
	if !o.json {
		fmt.Fprintln(out)
	}

	// Step 3: Generate context
	if !o.json {
		fmt.Fprintln(out, "[3/5] Generating context...")
	}
	if !o.skipContext {
		ctx, err := contextgen.GenerateContext(s)
		if err != nil {
			if o.json {
				jsonErrors = append(jsonErrors, fmt.Sprintf("context generation failed: %v", err))
			} else {
				return classifyErr("finish: context generation failed", err)
			}
		} else {
			if err := s.WriteContext(ctx); err != nil {
				if o.json {
					jsonErrors = append(jsonErrors, fmt.Sprintf("failed to write context.md: %v", err))
				} else {
					return classifyErr("finish: failed to write context.md", err)
				}
			} else {
				jsonOut.ContextUpdated = true
				if !o.json {
					fmt.Fprintln(out, "  context.md updated.")
				}
			}
		}
	} else {
		if !o.json {
			fmt.Fprintln(out, "  Skipped (--skip-context).")
		}
	}
	if !o.json {
		fmt.Fprintln(out)
	}

	// Step 4: Generate report
	if !o.json {
		fmt.Fprintln(out, "[4/5] Generating report...")
	}
	if !o.skipReport {
		report, err := reportgen.GenerateReport(s)
		if err != nil {
			if o.json {
				jsonErrors = append(jsonErrors, fmt.Sprintf("report generation failed: %v", err))
			} else {
				return classifyErr("finish: report generation failed", err)
			}
		} else {
			date := time.Now().Format("2006-01-02")
			reportPath := filepath.Join(s.ReportsDirPath(), date+"-report.md")
			if err := os.MkdirAll(s.ReportsDirPath(), 0755); err != nil {
				if o.json {
					jsonErrors = append(jsonErrors, fmt.Sprintf("failed to create reports dir: %v", err))
				} else {
					return classifyErr("finish: failed to create reports dir", err)
				}
			} else {
				if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
					if o.json {
						jsonErrors = append(jsonErrors, fmt.Sprintf("failed to write report: %v", err))
					} else {
						return classifyErr("finish: failed to write report", err)
					}
				} else {
					jsonOut.ReportSaved = reportPath
					if !o.json {
						fmt.Fprintf(out, "  Report saved: %s\n", reportPath)
					}
				}
			}
		}
	} else {
		if !o.json {
			fmt.Fprintln(out, "  Skipped (--skip-report).")
		}
	}
	if !o.json {
		fmt.Fprintln(out)
	}

	// Step 5: Next suggested task + session event
	if !o.json {
		fmt.Fprintln(out, "[5/5] Next suggested task...")
	}
	status, err := service.GetStatus(s)
	if err != nil {
		if o.json {
			jsonErrors = append(jsonErrors, fmt.Sprintf("status failed: %v", err))
		} else {
			return classifyErr("finish: status failed", err)
		}
	} else {
		if status.NextTask != nil {
			nextStr := fmt.Sprintf("%s - %s", status.NextTask.ID, status.NextTask.Title)
			jsonOut.NextTask = nextStr
			if !o.json {
				fmt.Fprintf(out, "  Next: %s\n", nextStr)
			}
		} else if status.Pending == 0 && status.InProgress == 0 && status.Blocked == 0 {
			if !o.json {
				fmt.Fprintln(out, "  All tasks complete. No next task.")
			}
		} else {
			if !o.json {
				fmt.Fprintln(out, "  No ready next task (dependencies unmet or all blocked).")
			}
		}
	}

	// Log session.finished event
	finishEvt := model.NewEvent(model.EventSessionFinished, "", "", "session finished")
	if o.agent != "" {
		finishEvt.Agent = o.agent
	}
	_ = s.AppendEvent(finishEvt)

	if o.json {
		if len(jsonErrors) > 0 {
			jsonOut.Errors = jsonErrors
		}
		if hasCheckIssues {
			// The JSON error envelope replaces the result document so stdout
			// always contains exactly one JSON document.
			return checkFailedError(result, o.strict)
		}
		data, err := json.MarshalIndent(jsonOut, "", "  ")
		if err != nil {
			return clierr.Wrap(clierr.KindOperation, err, "failed to marshal JSON")
		}
		fmt.Fprintln(out, string(data))
	} else {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "=== Session finished ===")
		if hasCheckIssues {
			return checkFailedError(result, o.strict)
		}
	}

	return nil
}
