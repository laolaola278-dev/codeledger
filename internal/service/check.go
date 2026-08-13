package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/codeledger/codeledger/internal/git"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// CheckStatus represents the outcome of a single check.
type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckFail CheckStatus = "fail"
	CheckWarn CheckStatus = "warn"
	CheckInfo CheckStatus = "info"
)

// CheckItem is a single consistency check result.
type CheckItem struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message,omitempty"`
}

// CheckResult holds the aggregate result of a consistency check.
type CheckResult struct {
	Passed bool        `json:"passed"`
	Checks []CheckItem `json:"checks"`
}

// HasFailures returns true if any check failed (status == fail).
func (r *CheckResult) HasFailures() bool {
	for _, c := range r.Checks {
		if c.Status == CheckFail {
			return true
		}
	}
	return false
}

// HasWarnings returns true if any check has status warn.
func (r *CheckResult) HasWarnings() bool {
	for _, c := range r.Checks {
		if c.Status == CheckWarn {
			return true
		}
	}
	return false
}

// add appends a check item to the result.
func (r *CheckResult) add(name string, status CheckStatus, msg string) {
	r.Checks = append(r.Checks, CheckItem{Name: name, Status: status, Message: msg})
	if status == CheckFail {
		r.Passed = false
	}
}

// taskIDPattern matches TASK-001 style IDs.
var taskIDPattern = regexp.MustCompile(`^[A-Z]+-\d+$`)

// RunCheck performs a full consistency check of the .ctask project state.
// It validates project.yaml, tasks.yaml, locks.yaml, evidence files,
// and cross-references between them.
func RunCheck(s *store.Store) *CheckResult {
	r := &CheckResult{Passed: true}

	// 1. project.yaml
	if p, err := s.ReadProject(); err != nil {
		r.add("project.yaml", CheckFail, fmt.Sprintf("cannot read: %v", err))
	} else {
		r.add("project.yaml", CheckPass, fmt.Sprintf("project %q loaded", p.Name))
	}

	// 2. tasks.yaml
	tl, err := s.ReadTasks()
	if err != nil {
		r.add("tasks.yaml", CheckFail, fmt.Sprintf("cannot read: %v", err))
		r.checkLocks(s, nil)
		r.checkEvents(s)
		return r
	}
	r.add("tasks.yaml", CheckPass, fmt.Sprintf("%d task(s) loaded", len(tl.Tasks)))

	// 3. Task IDs unique
	idSet := make(map[string]bool)
	dupIDs := false
	for _, t := range tl.Tasks {
		if idSet[t.ID] {
			r.add("task-ids-unique", CheckFail, fmt.Sprintf("duplicate task ID: %s", t.ID))
			dupIDs = true
		}
		idSet[t.ID] = true
	}
	if !dupIDs {
		r.add("task-ids-unique", CheckPass, "all task IDs unique")
	}

	// 4. Task statuses and priorities valid
	var invalidStatuses []string
	var invalidPriorities []string
	var doneMissingCompleted []string
	var invalidTestResults []string
	var invalidTaskIDs []string
	var selfDeps []string
	var doneNoFiles []string
	var doneUnknownResult []string
	var doneNoEvidence []string
	var blockedNoReason []string

	for _, t := range tl.Tasks {
		if !model.IsValidStatus(t.Status) {
			invalidStatuses = append(invalidStatuses, fmt.Sprintf("%s:%s", t.ID, t.Status))
		}
		if t.Priority != "" && !model.IsValidPriority(t.Priority) {
			invalidPriorities = append(invalidPriorities, fmt.Sprintf("%s:%s", t.ID, t.Priority))
		}
		if t.Status == model.StatusDone && t.CompletedAt == "" {
			doneMissingCompleted = append(doneMissingCompleted, t.ID)
		}

		// Task ID format check
		if !taskIDPattern.MatchString(t.ID) {
			invalidTaskIDs = append(invalidTaskIDs, t.ID)
		}

		// Self-dependency check
		for _, dep := range t.DependsOn {
			if dep == t.ID {
				selfDeps = append(selfDeps, t.ID)
			}
		}

		// Test result validity check
		if t.Test.Result != "" && !model.IsValidTestResult(t.Test.Result) {
			invalidTestResults = append(invalidTestResults, fmt.Sprintf("%s:%s", t.ID, t.Test.Result))
		}

		// Done task checks
		if t.Status == model.StatusDone {
			if len(t.Files) == 0 {
				doneNoFiles = append(doneNoFiles, t.ID)
			}
			if t.Test.Result == "" || t.Test.Result == model.TestResultUnknown {
				doneUnknownResult = append(doneUnknownResult, t.ID)
			}
			if len(t.Evidence) == 0 {
				doneNoEvidence = append(doneNoEvidence, t.ID)
			}
		}

		// Blocked task checks
		if t.Status == model.StatusBlocked && t.BlockedReason == "" {
			blockedNoReason = append(blockedNoReason, t.ID)
		}
	}

	if len(invalidStatuses) > 0 {
		r.add("task-status-valid", CheckFail, fmt.Sprintf("invalid status(es): %s", joinIDs(invalidStatuses)))
	} else {
		r.add("task-status-valid", CheckPass, "all statuses valid")
	}
	if len(invalidPriorities) > 0 {
		r.add("task-priority-valid", CheckFail, fmt.Sprintf("invalid priority(es): %s", joinIDs(invalidPriorities)))
	} else {
		r.add("task-priority-valid", CheckPass, "all priorities valid")
	}
	if len(doneMissingCompleted) > 0 {
		r.add("done-tasks-completed-at", CheckWarn, fmt.Sprintf("done task(s) missing completed_at: %s", joinIDs(doneMissingCompleted)))
	} else {
		r.add("done-tasks-completed-at", CheckPass, "all done tasks have completed_at")
	}
	if len(invalidTaskIDs) > 0 {
		r.add("task-id-format", CheckFail, fmt.Sprintf("task ID(s) not matching TASK-NNN format: %s", joinIDs(invalidTaskIDs)))
	} else {
		r.add("task-id-format", CheckPass, "all task IDs match TASK-NNN format")
	}
	if len(selfDeps) > 0 {
		r.add("task-no-self-dependency", CheckFail, fmt.Sprintf("task(s) depend on themselves: %s", joinIDs(selfDeps)))
	} else {
		r.add("task-no-self-dependency", CheckPass, "no self-dependencies")
	}
	if len(invalidTestResults) > 0 {
		r.add("task-test-result-valid", CheckFail, fmt.Sprintf("invalid test result(s): %s", joinIDs(invalidTestResults)))
	} else {
		r.add("task-test-result-valid", CheckPass, "all test results valid")
	}
	if len(doneNoFiles) > 0 {
		r.add("done-tasks-files", CheckWarn, fmt.Sprintf("done task(s) with no files: %s", joinIDs(doneNoFiles)))
	} else {
		r.add("done-tasks-files", CheckPass, "all done tasks have files")
	}
	if len(doneUnknownResult) > 0 {
		r.add("done-tasks-test-result", CheckWarn, fmt.Sprintf("done task(s) with unknown or missing test result: %s", joinIDs(doneUnknownResult)))
	} else {
		r.add("done-tasks-test-result", CheckPass, "all done tasks have test results")
	}
	if len(doneNoEvidence) > 0 {
		r.add("done-tasks-evidence", CheckWarn, fmt.Sprintf("done task(s) with no evidence: %s", joinIDs(doneNoEvidence)))
	} else {
		r.add("done-tasks-evidence", CheckPass, "all done tasks have evidence")
	}
	if len(blockedNoReason) > 0 {
		r.add("blocked-tasks-reason", CheckWarn, fmt.Sprintf("blocked task(s) with no reason: %s", joinIDs(blockedNoReason)))
	} else {
		r.add("blocked-tasks-reason", CheckPass, "all blocked tasks have reason")
	}

	// 5. Dependencies reference existing tasks
	var missingDeps []string
	var doneMissingDeps []string
	for _, t := range tl.Tasks {
		for _, dep := range t.DependsOn {
			if !idSet[dep] {
				if t.Status == model.StatusDone {
					doneMissingDeps = append(doneMissingDeps, fmt.Sprintf("%s->%s", t.ID, dep))
				} else {
					missingDeps = append(missingDeps, fmt.Sprintf("%s->%s", t.ID, dep))
				}
			}
		}
	}
	if len(missingDeps) > 0 {
		r.add("task-dependencies", CheckFail, fmt.Sprintf("missing dependency targets: %s", joinIDs(missingDeps)))
	} else {
		r.add("task-dependencies", CheckPass, "all dependencies reference existing tasks")
	}
	if len(doneMissingDeps) > 0 {
		r.add("done-task-dependencies", CheckFail, fmt.Sprintf("done task(s) with missing dependencies: %s", joinIDs(doneMissingDeps)))
	} else {
		r.add("done-task-dependencies", CheckPass, "all done task dependencies exist")
	}

	// 6. Evidence files exist on disk
	var missingEvidence []string
	for _, t := range tl.Tasks {
		for _, ev := range t.Evidence {
			evPath := filepath.Join(s.BasePath, filepath.FromSlash(ev))
			if _, err := os.Stat(evPath); errors.Is(err, os.ErrNotExist) {
				missingEvidence = append(missingEvidence, fmt.Sprintf("%s:%s", t.ID, ev))
			}
		}
	}
	if len(missingEvidence) > 0 {
		r.add("evidence-files", CheckWarn, fmt.Sprintf("missing evidence file(s): %s", joinIDs(missingEvidence)))
	} else {
		r.add("evidence-files", CheckPass, "all evidence files exist")
	}

	// 7. Evidence directory
	if err := s.EnsureEvidenceDir(); err != nil {
		r.add("evidence-dir", CheckWarn, fmt.Sprintf("cannot create evidence directory: %v", err))
	} else {
		evStat, err := os.Stat(s.EvidenceDirPath())
		if err != nil {
			r.add("evidence-dir", CheckWarn, "evidence directory not found")
		} else {
			r.add("evidence-dir", CheckPass, "evidence directory exists")
			_ = evStat
		}
	}

	// 8. Multiple in_progress tasks
	var inProgressIDs []string
	for _, t := range tl.Tasks {
		if t.Status == model.StatusInProgress {
			inProgressIDs = append(inProgressIDs, t.ID)
		}
	}
	if len(inProgressIDs) > 1 {
		r.add("multiple-in-progress", CheckWarn, fmt.Sprintf("multiple tasks in_progress: %s", joinIDs(inProgressIDs)))
	} else {
		r.add("multiple-in-progress", CheckPass, "at most one task in_progress")
	}

	// 7. Locks
	r.checkLocks(s, idSet)

	// 8. Events
	r.checkEvents(s)

	// 9. Git checks
	r.checkGit(s, tl)

	return r
}

// checkLocks validates locks.yaml and cross-references with tasks.
func (r *CheckResult) checkLocks(s *store.Store, taskIDs map[string]bool) {
	locks, err := s.ReadLocks()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.add("locks.yaml", CheckWarn, "locks.yaml not found")
			return
		}
		r.add("locks.yaml", CheckFail, fmt.Sprintf("cannot read: %v", err))
		return
	}
	r.add("locks.yaml", CheckPass, fmt.Sprintf("%d lock(s) loaded", len(locks.Locks)))

	var orphanLocks []string
	var expiredLocks []string
	var doneTaskLocks []string
	var multiLockTasks []string
	lockCount := make(map[string]int)

	for _, l := range locks.Locks {
		if taskIDs != nil && !taskIDs[l.TaskID] {
			orphanLocks = append(orphanLocks, l.TaskID)
		}
		if l.IsExpired() {
			expiredLocks = append(expiredLocks, l.TaskID)
		}
		// Count active (non-expired) locks per task
		if !l.IsExpired() {
			lockCount[l.TaskID]++
		}
	}
	// Check for multiple active locks on the same task
	for taskID, count := range lockCount {
		if count > 1 {
			multiLockTasks = append(multiLockTasks, taskID)
		}
	}
	// Check for done tasks with active locks (only if we have task statuses)
	if taskIDs != nil {
		tl, err := s.ReadTasks()
		if err == nil {
			for _, t := range tl.Tasks {
				if t.Status == model.StatusDone && lockCount[t.ID] > 0 {
					doneTaskLocks = append(doneTaskLocks, t.ID)
				}
			}
		}
	}

	if len(orphanLocks) > 0 {
		r.add("locks-task-exists", CheckFail, fmt.Sprintf("lock(s) for unknown task: %s", joinIDs(orphanLocks)))
	} else {
		r.add("locks-task-exists", CheckPass, "all locks reference existing tasks")
	}
	if len(expiredLocks) > 0 {
		r.add("locks-expired", CheckWarn, fmt.Sprintf("expired lock(s): %s", joinIDs(expiredLocks)))
	} else {
		r.add("locks-expired", CheckPass, "no expired locks")
	}
	if len(multiLockTasks) > 0 {
		r.add("locks-duplicate", CheckFail, fmt.Sprintf("multiple active locks on same task: %s", joinIDs(multiLockTasks)))
	} else {
		r.add("locks-duplicate", CheckPass, "no duplicate active locks")
	}
	if len(doneTaskLocks) > 0 {
		r.add("locks-done-task", CheckWarn, fmt.Sprintf("done task(s) still have active locks: %s", joinIDs(doneTaskLocks)))
	} else {
		r.add("locks-done-task", CheckPass, "no done tasks with active locks")
	}
}

// checkEvents validates that events.jsonl exists and is readable.
func (r *CheckResult) checkEvents(s *store.Store) {
	events, err := s.ReadEvents()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.add("events.jsonl", CheckWarn, "events.jsonl not found")
			return
		}
		r.add("events.jsonl", CheckFail, fmt.Sprintf("cannot read: %v", err))
		return
	}
	r.add("events.jsonl", CheckPass, fmt.Sprintf("%d event(s) logged", len(events)))
}

// joinIDs joins a string slice with commas for compact output.
func joinIDs(ids []string) string {
	return strings.Join(ids, ", ")
}

// checkGit validates git-related consistency.
// Non-git projects produce an INFO result (not an error).
// Changed files under .ctask/ are internal state and are filtered out.
// Changed source files not bound to any task.Files produce a WARN.
func (r *CheckResult) checkGit(s *store.Store, tl *model.TaskList) {
	root := filepath.Dir(s.BasePath)
	if !git.IsGitRepo(root) {
		r.add("git-repo", CheckInfo, "not a git repository (git checks skipped)")
		return
	}
	r.add("git-repo", CheckPass, "git repository detected")

	changed, err := git.ChangedFiles(root)
	if err != nil {
		r.add("git-unbound-changes", CheckWarn, fmt.Sprintf("cannot read changed files: %v", err))
		return
	}

	// Filter out .ctask/ internal files (state, evidence, reports, etc.)
	var external []string
	for _, f := range changed {
		if !strings.HasPrefix(filepath.ToSlash(f), store.DirName+"/") {
			external = append(external, f)
		}
	}

	if len(external) == 0 {
		r.add("git-unbound-changes", CheckPass, "no external changed files")
		return
	}

	// Build set of all files bound to any task
	bound := make(map[string]bool)
	for _, t := range tl.Tasks {
		for _, f := range t.Files {
			bound[f] = true
		}
	}

	var unbound []string
	for _, f := range external {
		if !bound[f] {
			unbound = append(unbound, f)
		}
	}

	if len(unbound) > 0 {
		r.add("git-unbound-changes", CheckWarn, fmt.Sprintf("changed file(s) not bound to any task: %s", joinIDs(unbound)))
	} else {
		r.add("git-unbound-changes", CheckPass, "all changed files are bound to tasks")
	}
}
