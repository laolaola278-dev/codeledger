package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
)

// Sentinel errors returned by the task service. Callers use errors.Is to
// classify failures without string matching.
var (
	// ErrTaskNotFound is wrapped when an operation references an unknown task.
	ErrTaskNotFound = errors.New("task not found")
	// ErrInvalidPriority is wrapped when AddTask receives a priority outside
	// low/medium/high.
	ErrInvalidPriority = errors.New("invalid priority")
)

// AddTask adds a new task to the project.
func AddTask(s *store.Store, title, description, priority string, dependsOn []string) (*model.Task, error) {
	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	existingIDs := make([]string, len(tl.Tasks))
	for i, t := range tl.Tasks {
		existingIDs[i] = t.ID
	}

	// Fail closed: an invalid priority is a caller error, never silently
	// downgraded to medium. The CLI maps this to VALIDATION_ERROR (exit 2).
	if !model.IsValidPriority(priority) {
		return nil, fmt.Errorf("%w: %q (must be low, medium, or high)", ErrInvalidPriority, priority)
	}

	now := util.NowRFC3339()
	task := model.Task{
		ID:          util.NextTaskID(existingIDs),
		Title:       title,
		Description: description,
		Status:      model.StatusPending,
		Priority:    priority,
		DependsOn:   dependsOn,
		Files:       []string{},
		Notes:       "",
		Test:        model.TaskTest{Result: model.TestResultUnknown},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	tl.Tasks = append(tl.Tasks, task)
	if err := s.WriteTasks(tl); err != nil {
		return nil, err
	}

	evt := model.NewEvent(model.EventTaskCreated, task.ID, task.Title, "")
	if err := s.AppendEvent(evt); err != nil {
		return nil, err
	}

	return &task, nil
}

// findTaskByID finds a task by ID in the task list. Returns the index and a pointer to the task.
func findTaskByID(tl *model.TaskList, id string) (int, *model.Task, error) {
	for i := range tl.Tasks {
		if tl.Tasks[i].ID == id {
			return i, &tl.Tasks[i], nil
		}
	}
	return -1, nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
}

// StartTask sets a task's status to in_progress.
//
// Lease contract: when a lock record exists for the task (active, expired, or
// legacy), start requires the exact owner (--agent + --lease-id) or an
// explicit --force --reason --agent override. With no record, start keeps its
// original no-lease compatibility behavior (no flags required).
func StartTask(s *store.Store, clk clock.Clock, taskID string, auth lease.Auth) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}
	res, err := gateLease(locks, clk.Now(), taskID, auth, false)
	if err != nil {
		return err
	}

	tl, err := s.ReadTasks()
	if err != nil {
		return err
	}

	idx, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return err
	}

	if task.Status == model.StatusDone {
		return fmt.Errorf("task %s is already completed", taskID)
	}

	// Check dependencies
	for _, depID := range task.DependsOn {
		_, dep, err := findTaskByID(tl, depID)
		if err != nil {
			return fmt.Errorf("dependency %s not found for task %s", depID, taskID)
		}
		if dep.Status != model.StatusDone {
			return fmt.Errorf("dependency %s (%s) is not completed yet", depID, dep.Title)
		}
	}

	task.Status = model.StatusInProgress
	task.UpdatedAt = clk.Now().UTC().Format(time.RFC3339)
	tl.Tasks[idx] = *task

	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	// Audit a forced override without touching the lease itself (start never
	// removes or modifies the existing lease).
	if res.forced {
		return recordForcedOverride(s, taskID, task.Title, auth, res.entry)
	}

	evt := model.NewEvent(model.EventTaskStarted, taskID, task.Title, "")
	if auth.Agent != "" {
		evt.Agent = auth.Agent
	}
	return s.AppendEvent(evt)
}

// CompleteOptions carries the optional metadata for CompleteTask.
type CompleteOptions struct {
	Files       string
	Test        string
	Result      string
	Note        string
	AutoFiles   bool
	CaptureDiff bool
	// Agent is the completing agent. When the task has a lock record, Agent
	// and LeaseID must both match the record exactly, or Force must be set
	// with an explicit Reason.
	Agent   string
	LeaseID string
	Force   bool
	Reason  string
}

// CompleteTask marks a task as done with optional metadata.
// If autoFiles is true, changed files are detected from Git and merged
// with any explicitly provided --files (deduplicated).
// If captureDiff is true, the full Git diff is saved to a separate .diff
// evidence file and the path is added to Task.Evidence.
//
// Lease contract: completion of a task with a lock record requires the exact
// owner (opts.Agent + opts.LeaseID), or --force --reason --agent to override.
// Legacy and expired records are fail-closed (exit 3); they are never
// silently cleaned by an ordinary completion. A successful completion removes
// only the just-verified target lease.
func CompleteTask(s *store.Store, clk clock.Clock, taskID string, opts CompleteOptions) error {
	// Validate the lease BEFORE doing any completion work so a blocked
	// completion never leaves partial state (evidence, status, events).
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}
	nowT := clk.Now()
	res, err := gateLease(locks, nowT, taskID, lease.Auth{
		Agent:   opts.Agent,
		LeaseID: opts.LeaseID,
		Force:   opts.Force,
		Reason:  opts.Reason,
	}, false)
	if err != nil {
		return err
	}
	// Immutable snapshot of the prior record BEFORE the completion removes
	// the lease, so the forced-override audit carries the true previous
	// owner/lease/state (classified under the same now as the gate).
	prior := snapshotLock(res.entry, nowT)

	tl, err := s.ReadTasks()
	if err != nil {
		return err
	}

	idx, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return err
	}

	if task.Status == model.StatusDone {
		return fmt.Errorf("task %s is already completed", taskID)
	}

	if opts.Result != "" && !model.IsValidTestResult(opts.Result) {
		return fmt.Errorf("invalid test result: %s (must be passed, failed, skipped, or unknown)", opts.Result)
	}

	now := nowT.UTC().Format(time.RFC3339)
	task.Status = model.StatusDone
	task.UpdatedAt = now
	task.CompletedAt = now

	// Collect files: preserve existing, add explicit --files
	fileSet := append([]string{}, task.Files...)
	if opts.Files != "" {
		fileSet = append(fileSet, util.SplitCommas(opts.Files)...)
	}

	gitDir := filepath.Dir(s.BasePath)

	// Scan git for evidence metadata (only if needed)
	var gitEv *GitEvidence
	if opts.AutoFiles || opts.CaptureDiff {
		gitEv = scanGitProject(gitDir)
	} else {
		gitEv = &GitEvidence{Error: "not captured"}
	}

	// Auto-detect changed files from Git and merge with dedup
	if opts.AutoFiles {
		if gitEv.Error != "" {
			return fmt.Errorf("--auto-files requires a git repository")
		}
		fileSet = append(fileSet, gitEv.ChangedFiles...)
	}
	task.Files = dedupStrings(fileSet)

	if opts.Test != "" {
		task.Test.Command = opts.Test
	}
	if opts.Result != "" {
		task.Test.Result = opts.Result
	}

	if opts.Note != "" {
		if task.Notes != "" {
			task.Notes += "\n" + opts.Note
		} else {
			task.Notes = opts.Note
		}
	}

	if !opts.CaptureDiff {
		gitEv.Diff = ""
		gitEv.DiffStat = ""
	}

	// Build evidence paths: always .md, optionally .diff
	evidencePaths := []string{s.EvidenceRelPath(taskID)}

	// Capture diff to separate .diff file
	if opts.CaptureDiff {
		if gitEv.Error != "" {
			return fmt.Errorf("--capture-diff requires a git repository")
		}
		diffContent := gitEv.Diff
		if diffContent == "" {
			diffContent = "No git diff captured."
		}
		if err := s.EnsureEvidenceDir(); err != nil {
			return err
		}
		if err := os.WriteFile(s.EvidenceDiffPath(taskID), []byte(diffContent), 0644); err != nil {
			return fmt.Errorf("failed to write diff evidence: %w", err)
		}
		evidencePaths = append(evidencePaths, s.EvidenceDiffRelPath(taskID))
		diffEvt := model.NewEvent(model.EventDiffCaptured, taskID, task.Title, "diff captured to "+s.EvidenceDiffRelPath(taskID))
		if err := s.AppendEvent(diffEvt); err != nil {
			return err
		}
	}

	task.Evidence = evidencePaths

	// Record markdown evidence (.md file)
	if err := recordEvidence(s, task, gitEv); err != nil {
		return err
	}

	// Log files attached event
	if len(task.Files) > 0 {
		filesEvt := model.NewEvent(model.EventFilesAttached, taskID, task.Title, fmt.Sprintf("%d file(s) attached", len(task.Files)))
		if err := s.AppendEvent(filesEvt); err != nil {
			return err
		}
	}

	tl.Tasks[idx] = *task
	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventTaskCompleted, taskID, task.Title, opts.Note)
	if err := s.AppendEvent(evt); err != nil {
		return err
	}

	// Auto-release the lease if the task had a record. The gate already
	// verified the exact lease (owner path) or authorized the override (force
	// path), so removing the single target entry removes exactly the verified
	// lease and never touches another task.
	if res.entry != nil {
		if err := removeLease(s, taskID); err != nil {
			return err
		}
		if res.forced {
			return recordForcedOverrideAudit(s, taskID, task.Title, lease.Auth{Agent: opts.Agent, Reason: opts.Reason}, prior, "lease removed")
		}
		releasedEvt := model.NewEvent(model.EventLockReleasedOnDone, taskID, "", "")
		if err := s.AppendEvent(releasedEvt); err != nil {
			return err
		}
	}

	return nil
}

// removeLease removes only the lock entry for the given task, preserving all
// other entries (active, expired, or legacy) untouched.
func removeLease(s *store.Store, taskID string) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}
	removeTaskLock(locks, taskID)
	return s.WriteLocks(locks)
}

// recordForcedOverride appends a task.lease_broken audit event recording the
// actor, reason, and the previous owner/lease/state. It is used by mutations
// that honor a --force override without removing the existing lease (start /
// block / note): the fence is overridden for this one mutation but the lease
// itself is retained so other agents remain fenced out.
func recordForcedOverride(s *store.Store, taskID, title string, auth lease.Auth, entry *model.Lock) error {
	return recordForcedOverrideAudit(s, taskID, title, auth, snapshotLock(entry, time.Now()), "lease retained")
}

// BlockTask sets a task's status to blocked with a reason.
//
// Lease contract: identical to StartTask - a lock record requires exact owner
// credentials or --force --reason --agent; no record keeps the compatibility
// path. Blocking never removes the existing lease.
func BlockTask(s *store.Store, clk clock.Clock, taskID, reason string, auth lease.Auth) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}
	res, err := gateLease(locks, clk.Now(), taskID, auth, false)
	if err != nil {
		return err
	}

	tl, err := s.ReadTasks()
	if err != nil {
		return err
	}

	idx, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return err
	}

	if task.Status == model.StatusDone {
		return fmt.Errorf("cannot block task %s: it is already completed", taskID)
	}

	task.Status = model.StatusBlocked
	task.BlockedReason = reason
	task.UpdatedAt = clk.Now().UTC().Format(time.RFC3339)
	tl.Tasks[idx] = *task

	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	if res.forced {
		return recordForcedOverride(s, taskID, task.Title, auth, res.entry)
	}

	evt := model.NewEvent(model.EventTaskBlocked, taskID, task.Title, reason)
	if auth.Agent != "" {
		evt.Agent = auth.Agent
	}
	return s.AppendEvent(evt)
}

// NoteTask appends a note to a task without changing its status.
//
// Lease contract: identical to StartTask - a lock record requires exact owner
// credentials or --force --reason --agent; no record keeps the compatibility
// path. Noting never removes the existing lease.
func NoteTask(s *store.Store, clk clock.Clock, taskID, note string, auth lease.Auth) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}
	res, err := gateLease(locks, clk.Now(), taskID, auth, false)
	if err != nil {
		return err
	}

	tl, err := s.ReadTasks()
	if err != nil {
		return err
	}

	idx, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return err
	}

	if task.Notes != "" {
		task.Notes += "\n" + note
	} else {
		task.Notes = note
	}
	task.UpdatedAt = clk.Now().UTC().Format(time.RFC3339)
	tl.Tasks[idx] = *task

	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	if res.forced {
		return recordForcedOverride(s, taskID, task.Title, auth, res.entry)
	}

	evt := model.NewEvent(model.EventTaskNoted, taskID, task.Title, note)
	if auth.Agent != "" {
		evt.Agent = auth.Agent
	}
	return s.AppendEvent(evt)
}

// StatusSummary holds aggregated project status information.
type StatusSummary struct {
	ProjectName  string
	Total        int
	Pending      int
	InProgress   int
	Done         int
	Blocked      int
	CurrentTask  *model.Task
	BlockedTasks []model.Task
	NextTask     *model.Task
}

// GetStatus computes a summary of the project's current status.
func GetStatus(s *store.Store) (*StatusSummary, error) {
	p, err := s.ReadProject()
	if err != nil {
		return nil, err
	}

	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	summary := &StatusSummary{
		ProjectName: p.Name,
	}
	summary.Total = len(tl.Tasks)

	for i := range tl.Tasks {
		t := tl.Tasks[i]
		switch t.Status {
		case model.StatusPending:
			summary.Pending++
		case model.StatusInProgress:
			summary.InProgress++
			summary.CurrentTask = &tl.Tasks[i]
		case model.StatusDone:
			summary.Done++
		case model.StatusBlocked:
			summary.Blocked++
			summary.BlockedTasks = append(summary.BlockedTasks, tl.Tasks[i])
		}
	}

	summary.NextTask = findNextSuggestedTask(tl.Tasks)

	return summary, nil
}

// findNextSuggestedTask finds the first pending task whose dependencies are all done.
func findNextSuggestedTask(tasks []model.Task) *model.Task {
	doneSet := make(map[string]bool)
	for _, t := range tasks {
		if t.Status == model.StatusDone {
			doneSet[t.ID] = true
		}
	}

	sorted := make([]model.Task, len(tasks))
	copy(sorted, tasks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	for _, t := range sorted {
		if t.Status != model.StatusPending {
			continue
		}
		allDepsDone := true
		for _, depID := range t.DependsOn {
			if !doneSet[depID] {
				allDepsDone = false
				break
			}
		}
		if allDepsDone {
			return &t
		}
	}

	return nil
}

// GetTaskByID retrieves a single task by ID.
func GetTaskByID(s *store.Store, taskID string) (*model.Task, error) {
	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}
	_, task, err := findTaskByID(tl, taskID)
	return task, err
}

// ListTasks returns all tasks, optionally filtered by status.
func ListTasks(s *store.Store, statusFilter string) ([]model.Task, error) {
	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	if statusFilter == "" {
		return tl.Tasks, nil
	}

	if !model.IsValidStatus(statusFilter) {
		return nil, fmt.Errorf("invalid status filter: %s", statusFilter)
	}

	var filtered []model.Task
	for _, t := range tl.Tasks {
		if t.Status == statusFilter {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// GetRecentDoneTasks returns the most recently completed tasks (up to n).
func GetRecentDoneTasks(s *store.Store, n int) ([]model.Task, error) {
	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	var done []model.Task
	for _, t := range tl.Tasks {
		if t.Status == model.StatusDone && t.CompletedAt != "" {
			done = append(done, t)
		}
	}

	sort.Slice(done, func(i, j int) bool {
		return done[i].CompletedAt > done[j].CompletedAt
	})

	if len(done) > n {
		done = done[:n]
	}

	return done, nil
}

// GetModifiedFiles returns a deduplicated list of all files modified across done tasks.
func GetModifiedFiles(s *store.Store) ([]string, error) {
	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var files []string
	for _, t := range tl.Tasks {
		for _, f := range t.Files {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}

	return files, nil
}

// GetTestResults returns a summary of test results across done tasks.
func GetTestResults(s *store.Store) ([]model.Task, error) {
	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	var results []model.Task
	for _, t := range tl.Tasks {
		if t.Test.Command != "" || t.Test.Result != model.TestResultUnknown {
			results = append(results, t)
		}
	}

	return results, nil
}
