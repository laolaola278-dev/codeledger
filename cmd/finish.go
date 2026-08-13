package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/codeledger/codeledger/internal/contextgen"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/reportgen"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	finishTask        string
	finishFiles       string
	finishTest        string
	finishResult      string
	finishNote        string
	finishAgent       string
	finishAutoFiles   bool
	finishCaptureDiff bool
	finishSkipReport  bool
	finishSkipContext bool
	finishStrict      bool
	finishJSON        bool
)

// finishJSONOutput is the JSON structure for --json output.
type finishJSONOutput struct {
	Check          *service.CheckResult `json:"check,omitempty"`
	TaskCompleted  string               `json:"task_completed"`
	ContextUpdated bool                 `json:"context_updated"`
	ReportSaved    string               `json:"report_saved,omitempty"`
	NextTask       string               `json:"next_task,omitempty"`
	Errors         []string             `json:"errors,omitempty"`
}

var finishCmd = &cobra.Command{
	Use:   "finish",
	Short: "End an agent session: check, complete task, generate context and report",
	Long: `Run a session finish sequence.

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
  --json              Output results as JSON`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		exitNonZero := false
		err := withProjectLock(s, "finish", finishAgent, finishTask, func() error {
			return runFinish(s, &exitNonZero)
		})
		if exitNonZero {
			os.Exit(1)
		}
		return err
	},
}

// runFinish executes the finish sequence while the project mutation lock is held.
// The project lock is released (deferred) before this function's result is acted
// upon, so os.Exit for --strict mode must happen outside the locked section.
func runFinish(s *store.Store, exitNonZero *bool) error {
	var jsonOut finishJSONOutput
	var jsonErrors []string

	if !finishJSON {
		fmt.Println("=== CodeLedger Session Finish ===")
		fmt.Println()
	}

	// Step 1: Check
	if !finishJSON {
		fmt.Println("[1/5] Running consistency check...")
	}
	result := service.RunCheck(s)
	jsonOut.Check = result

	hasCheckIssues := result.HasFailures()
	if finishStrict && result.HasWarnings() {
		hasCheckIssues = true
	}

	if !finishJSON {
		printCheckResult(result, false)
	}

	var evtType string
	if result.HasFailures() {
		evtType = model.EventCheckFailed
		if !finishJSON {
			fmt.Println("WARNING: check found failures. Continuing finish sequence.")
		}
	} else {
		evtType = model.EventCheckPassed
	}
	checkEvt := model.NewEvent(evtType, "", "", fmt.Sprintf("check: %d checks", len(result.Checks)))
	if finishAgent != "" {
		checkEvt.Agent = finishAgent
	}
	_ = s.AppendEvent(checkEvt)
	if !finishJSON {
		fmt.Println()
	}

	// Step 2: Optionally complete a task
	if finishTask != "" {
		if finishResult == "" {
			if !finishJSON {
				fmt.Printf("[2/5] Task %s not completed: --result is required to complete a task (use --result passed|failed|skipped|unknown).\n", finishTask)
			}
		} else {
			if !finishJSON {
				fmt.Printf("[2/5] Completing task %s...\n", finishTask)
			}
			if err := service.CompleteTask(s, finishTask, finishFiles, finishTest, finishResult, finishNote, finishAutoFiles, finishCaptureDiff); err != nil {
				if finishJSON {
					jsonErrors = append(jsonErrors, fmt.Sprintf("failed to complete task %s: %v", finishTask, err))
				} else {
					return fmt.Errorf("finish: failed to complete task %s: %w", finishTask, err)
				}
			} else {
				jsonOut.TaskCompleted = finishTask
				if !finishJSON {
					fmt.Printf("  Completed task %s.\n", finishTask)
				}
			}
		}
	} else {
		if !finishJSON {
			fmt.Println("[2/5] No task to complete (--task not specified).")
		}
	}
	if !finishJSON {
		fmt.Println()
	}

	// Step 3: Generate context
	if !finishJSON {
		fmt.Println("[3/5] Generating context...")
	}
	if !finishSkipContext {
		ctx, err := contextgen.GenerateContext(s)
		if err != nil {
			if finishJSON {
				jsonErrors = append(jsonErrors, fmt.Sprintf("context generation failed: %v", err))
			} else {
				return fmt.Errorf("finish: context generation failed: %w", err)
			}
		} else {
			if err := s.WriteContext(ctx); err != nil {
				if finishJSON {
					jsonErrors = append(jsonErrors, fmt.Sprintf("failed to write context.md: %v", err))
				} else {
					return fmt.Errorf("finish: failed to write context.md: %w", err)
				}
			} else {
				jsonOut.ContextUpdated = true
				if !finishJSON {
					fmt.Println("  context.md updated.")
				}
			}
		}
	} else {
		if !finishJSON {
			fmt.Println("  Skipped (--skip-context).")
		}
	}
	if !finishJSON {
		fmt.Println()
	}

	// Step 4: Generate report
	if !finishJSON {
		fmt.Println("[4/5] Generating report...")
	}
	if !finishSkipReport {
		report, err := reportgen.GenerateReport(s)
		if err != nil {
			if finishJSON {
				jsonErrors = append(jsonErrors, fmt.Sprintf("report generation failed: %v", err))
			} else {
				return fmt.Errorf("finish: report generation failed: %w", err)
			}
		} else {
			date := time.Now().Format("2006-01-02")
			reportPath := filepath.Join(s.ReportsDirPath(), date+"-report.md")
			if err := os.MkdirAll(s.ReportsDirPath(), 0755); err != nil {
				if finishJSON {
					jsonErrors = append(jsonErrors, fmt.Sprintf("failed to create reports dir: %v", err))
				} else {
					return fmt.Errorf("finish: failed to create reports dir: %w", err)
				}
			} else {
				if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
					if finishJSON {
						jsonErrors = append(jsonErrors, fmt.Sprintf("failed to write report: %v", err))
					} else {
						return fmt.Errorf("finish: failed to write report: %w", err)
					}
				} else {
					jsonOut.ReportSaved = reportPath
					if !finishJSON {
						fmt.Printf("  Report saved: %s\n", reportPath)
					}
				}
			}
		}
	} else {
		if !finishJSON {
			fmt.Println("  Skipped (--skip-report).")
		}
	}
	if !finishJSON {
		fmt.Println()
	}

	// Step 5: Next suggested task + session event
	if !finishJSON {
		fmt.Println("[5/5] Next suggested task...")
	}
	status, err := service.GetStatus(s)
	if err != nil {
		if finishJSON {
			jsonErrors = append(jsonErrors, fmt.Sprintf("status failed: %v", err))
		} else {
			return fmt.Errorf("finish: status failed: %w", err)
		}
	} else {
		if status.NextTask != nil {
			nextStr := fmt.Sprintf("%s - %s", status.NextTask.ID, status.NextTask.Title)
			jsonOut.NextTask = nextStr
			if !finishJSON {
				fmt.Printf("  Next: %s\n", nextStr)
			}
		} else if status.Pending == 0 && status.InProgress == 0 && status.Blocked == 0 {
			if !finishJSON {
				fmt.Println("  All tasks complete. No next task.")
			}
		} else {
			if !finishJSON {
				fmt.Println("  No ready next task (dependencies unmet or all blocked).")
			}
		}
	}

	// Log session.finished event
	finishEvt := model.NewEvent(model.EventSessionFinished, "", "", "session finished")
	if finishAgent != "" {
		finishEvt.Agent = finishAgent
	}
	_ = s.AppendEvent(finishEvt)

	if finishJSON {
		if len(jsonErrors) > 0 {
			jsonOut.Errors = jsonErrors
		}
		out, err := json.MarshalIndent(jsonOut, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(out))
	} else {
		fmt.Println()
		fmt.Println("=== Session finished ===")
	}

	// Exit code: strict mode triggers non-zero on warnings too
	if hasCheckIssues {
		*exitNonZero = true
	}

	return nil
}

func init() {
	finishCmd.Flags().StringVar(&finishTask, "task", "", "Task ID to complete before finishing")
	finishCmd.Flags().StringVar(&finishAgent, "agent", "", "Agent name performing the finish")
	finishCmd.Flags().StringVar(&finishFiles, "files", "", "Comma-separated modified files (with --task)")
	finishCmd.Flags().StringVar(&finishTest, "test", "", "Test command that was run (with --task)")
	finishCmd.Flags().StringVar(&finishResult, "result", "", "Test result: passed, failed, skipped, unknown (with --task)")
	finishCmd.Flags().StringVar(&finishNote, "note", "", "Completion note (with --task)")
	finishCmd.Flags().BoolVar(&finishAutoFiles, "auto-files", false, "Auto-detect changed files from Git (with --task)")
	finishCmd.Flags().BoolVar(&finishCaptureDiff, "capture-diff", false, "Capture full Git diff (with --task)")
	finishCmd.Flags().BoolVar(&finishSkipContext, "skip-context", false, "Do not regenerate context.md")
	finishCmd.Flags().BoolVar(&finishSkipReport, "skip-report", false, "Do not generate a report file")
	finishCmd.Flags().BoolVar(&finishStrict, "strict", false, "Treat check warnings as failures (exit code 1)")
	finishCmd.Flags().BoolVar(&finishJSON, "json", false, "Output results as JSON")
	rootCmd.AddCommand(finishCmd)
}
