package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
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

	if !model.IsValidPriority(priority) {
		priority = model.PriorityMedium
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
	return -1, nil, fmt.Errorf("task %s not found", id)
}

// StartTask sets a task's status to in_progress.
func StartTask(s *store.Store, taskID string) error {
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
	task.UpdatedAt = util.NowRFC3339()
	tl.Tasks[idx] = *task

	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventTaskStarted, taskID, task.Title, "")
	return s.AppendEvent(evt)
}

// CompleteTask marks a task as done with optional metadata.
// If autoFiles is true, changed files are detected from Git and merged
// with any explicitly provided --files (deduplicated).
// If captureDiff is true, the full Git diff is saved to a separate .diff
// evidence file and the path is added to Task.Evidence.
func CompleteTask(s *store.Store, taskID, files, testCmd, testResult, note string, autoFiles bool, captureDiff bool) error {
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

	if testResult != "" && !model.IsValidTestResult(testResult) {
		return fmt.Errorf("invalid test result: %s (must be passed, failed, skipped, or unknown)", testResult)
	}

	now := util.NowRFC3339()
	task.Status = model.StatusDone
	task.UpdatedAt = now
	task.CompletedAt = now

	// Collect files: preserve existing, add explicit --files
	fileSet := append([]string{}, task.Files...)
	if files != "" {
		fileSet = append(fileSet, util.SplitCommas(files)...)
	}

	gitDir := filepath.Dir(s.BasePath)

	// Scan git for evidence metadata (only if needed)
	var gitEv *GitEvidence
	if autoFiles || captureDiff {
		gitEv = scanGitProject(gitDir)
	} else {
		gitEv = &GitEvidence{Error: "not captured"}
	}

	// Auto-detect changed files from Git and merge with dedup
	if autoFiles {
		if gitEv.Error != "" {
			return fmt.Errorf("--auto-files requires a git repository")
		}
		fileSet = append(fileSet, gitEv.ChangedFiles...)
	}
	task.Files = dedupStrings(fileSet)

	if testCmd != "" {
		task.Test.Command = testCmd
	}
	if testResult != "" {
		task.Test.Result = testResult
	}

	if note != "" {
		if task.Notes != "" {
			task.Notes += "\n" + note
		} else {
			task.Notes = note
		}
	}

	if !captureDiff {
		gitEv.Diff = ""
		gitEv.DiffStat = ""
	}

	// Build evidence paths: always .md, optionally .diff
	evidencePaths := []string{s.EvidenceRelPath(taskID)}

	// Capture diff to separate .diff file
	if captureDiff {
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

	evt := model.NewEvent(model.EventTaskCompleted, taskID, task.Title, note)
	if err := s.AppendEvent(evt); err != nil {
		return err
	}

	// Auto-release lock if task was claimed
	return releaseLockIfClaimed(s, taskID)
}

// BlockTask sets a task's status to blocked with a reason.
func BlockTask(s *store.Store, taskID, reason string) error {
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
	task.UpdatedAt = util.NowRFC3339()
	tl.Tasks[idx] = *task

	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventTaskBlocked, taskID, task.Title, reason)
	return s.AppendEvent(evt)
}

// NoteTask appends a note to a task without changing its status.
func NoteTask(s *store.Store, taskID, note string) error {
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
	task.UpdatedAt = util.NowRFC3339()
	tl.Tasks[idx] = *task

	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventTaskNoted, taskID, task.Title, note)
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

// releaseLockIfClaimed removes any active lock for the given task and logs an event.
func releaseLockIfClaimed(s *store.Store, taskID string) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return nil
	}

	found := false
	var active []model.Lock
	for _, l := range locks.Locks {
		if l.TaskID == taskID {
			found = true
			continue
		}
		active = append(active, l)
	}
	locks.Locks = active

	if !found {
		return nil
	}

	if err := s.WriteLocks(locks); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventLockReleasedOnDone, taskID, "", "")
	return s.AppendEvent(evt)
}
