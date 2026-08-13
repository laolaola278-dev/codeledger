package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// findCheck returns the first check item with the given name, or nil.
func findCheck(r *CheckResult, name string) *CheckItem {
	for i := range r.Checks {
		if r.Checks[i].Name == name {
			return &r.Checks[i]
		}
	}
	return nil
}

// writeRawTasks writes a TaskList directly, bypassing AddTask validation.
func writeRawTasks(t *testing.T, s *store.Store, tasks []model.Task) {
	t.Helper()
	if err := s.WriteTasks(&model.TaskList{Tasks: tasks}); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}
}

// writeRawLocks writes a LockList directly.
func writeRawLocks(t *testing.T, s *store.Store, locks []model.Lock) {
	t.Helper()
	if err := s.WriteLocks(&model.LockList{Locks: locks}); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}
}

func TestRunCheck_CleanProjectPasses(t *testing.T) {
	s, _ := setupTestStore(t)
	addTestTask(t, s, "Task A", model.PriorityHigh, nil)
	evt := model.NewEvent(model.EventTaskCreated, "TASK-001", "Task A", "")
	s.AppendEvent(evt)

	r := RunCheck(s)

	if r.HasFailures() {
		t.Fatalf("expected no failures on clean project, got: %+v", r.Checks)
	}
	if !r.Passed {
		t.Error("expected Passed=true on clean project")
	}
	for _, c := range r.Checks {
		if c.Status == CheckFail {
			t.Errorf("unexpected failure: %s - %s", c.Name, c.Message)
		}
	}
}

func TestRunCheck_DuplicateTaskIDs(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "First", Status: model.StatusPending, Priority: model.PriorityMedium},
		{ID: "TASK-001", Title: "Duplicate", Status: model.StatusPending, Priority: model.PriorityMedium},
	})

	r := RunCheck(s)

	c := findCheck(r, "task-ids-unique")
	if c == nil {
		t.Fatal("expected task-ids-unique check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for duplicate IDs, got %s", c.Status)
	}
	if !r.HasFailures() {
		t.Error("expected HasFailures=true")
	}
}

func TestRunCheck_InvalidStatus(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Bad1", Status: "invalid1", Priority: model.PriorityMedium},
		{ID: "TASK-002", Title: "Bad2", Status: "invalid2", Priority: model.PriorityMedium},
	})

	r := RunCheck(s)

	c := findCheck(r, "task-status-valid")
	if c == nil {
		t.Fatal("expected task-status-valid check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for invalid status, got %s", c.Status)
	}
	// Multiple invalid statuses should be collected into a single check item
	count := 0
	for _, ch := range r.Checks {
		if ch.Name == "task-status-valid" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 task-status-valid item, got %d", count)
	}
}

func TestRunCheck_InvalidPriority(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Bad priority", Status: model.StatusPending, Priority: "urgent"},
	})

	r := RunCheck(s)

	c := findCheck(r, "task-priority-valid")
	if c == nil {
		t.Fatal("expected task-priority-valid check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for invalid priority, got %s", c.Status)
	}
}

func TestRunCheck_DoneTaskMissingCompletedAt(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Done no ts", Status: model.StatusDone, Priority: model.PriorityMedium, CompletedAt: ""},
	})

	r := RunCheck(s)

	c := findCheck(r, "done-tasks-completed-at")
	if c == nil {
		t.Fatal("expected done-tasks-completed-at check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for missing completed_at, got %s", c.Status)
	}
	// Warning, not failure
	if r.HasFailures() {
		t.Error("expected no failures for missing completed_at (warning only)")
	}
}

func TestRunCheck_MissingDependency(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Missing dep", Status: model.StatusPending, Priority: model.PriorityMedium, DependsOn: []string{"TASK-999"}},
	})

	r := RunCheck(s)

	c := findCheck(r, "task-dependencies")
	if c == nil {
		t.Fatal("expected task-dependencies check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for missing dependency, got %s", c.Status)
	}
}

func TestRunCheck_MissingEvidenceFile(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Missing ev", Status: model.StatusDone, Priority: model.PriorityMedium, CompletedAt: "2025-01-01T00:00:00Z", Evidence: []string{"evidence/TASK-001.md"}},
	})

	r := RunCheck(s)

	c := findCheck(r, "evidence-files")
	if c == nil {
		t.Fatal("expected evidence-files check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for missing evidence file, got %s", c.Status)
	}
}

func TestRunCheck_ExistingEvidenceFile(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Evidence task", model.PriorityMedium, nil)
	if err := CompleteTask(s, task.ID, "main.go", "go test ./...", model.TestResultPassed, "done", false, false); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	r := RunCheck(s)

	c := findCheck(r, "evidence-files")
	if c == nil {
		t.Fatal("expected evidence-files check")
	}
	if c.Status != CheckPass {
		t.Errorf("expected pass for existing evidence, got %s - %s", c.Status, c.Message)
	}
}

func TestRunCheck_OrphanLock(t *testing.T) {
	s, _ := setupTestStore(t)
	addTestTask(t, s, "Real task", model.PriorityMedium, nil)
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)
	writeRawLocks(t, s, []model.Lock{
		{TaskID: "TASK-999", Agent: "ghost", Role: "dev", AcquiredAt: now, ExpiresAt: future, HeartbeatAt: now},
	})

	r := RunCheck(s)

	c := findCheck(r, "locks-task-exists")
	if c == nil {
		t.Fatal("expected locks-task-exists check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for orphan lock, got %s", c.Status)
	}
}

func TestRunCheck_ExpiredLock(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Locked task", model.PriorityMedium, nil)
	past := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	writeRawLocks(t, s, []model.Lock{
		{TaskID: task.ID, Agent: "old", Role: "dev", AcquiredAt: past, ExpiresAt: past, HeartbeatAt: past},
	})

	r := RunCheck(s)

	c := findCheck(r, "locks-expired")
	if c == nil {
		t.Fatal("expected locks-expired check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for expired lock, got %s", c.Status)
	}
}

func TestRunCheck_ValidLocksPass(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Locked task", model.PriorityMedium, nil)
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)
	writeRawLocks(t, s, []model.Lock{
		{TaskID: task.ID, Agent: "agent1", Role: "dev", AcquiredAt: now, ExpiresAt: future, HeartbeatAt: now},
	})

	r := RunCheck(s)

	for _, name := range []string{"locks.yaml", "locks-task-exists", "locks-expired"} {
		c := findCheck(r, name)
		if c == nil {
			t.Fatalf("expected %s check", name)
		}
		if c.Status != CheckPass {
			t.Errorf("expected pass for %s, got %s - %s", name, c.Status, c.Message)
		}
	}
}

func TestRunCheck_EventsPresent(t *testing.T) {
	s, _ := setupTestStore(t)
	s.AppendEvent(model.NewEvent(model.EventTaskCreated, "", "test", ""))

	r := RunCheck(s)

	c := findCheck(r, "events.jsonl")
	if c == nil {
		t.Fatal("expected events.jsonl check")
	}
	if c.Status != CheckPass {
		t.Errorf("expected pass for events.jsonl, got %s - %s", c.Status, c.Message)
	}
}

func TestRunCheck_MissingEventsFile(t *testing.T) {
	s, _ := setupTestStore(t)
	os.Remove(s.EventsPath())

	r := RunCheck(s)

	c := findCheck(r, "events.jsonl")
	if c == nil {
		t.Fatal("expected events.jsonl check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for missing events.jsonl, got %s", c.Status)
	}
}

func TestRunCheck_MissingLocksFile(t *testing.T) {
	s, _ := setupTestStore(t)
	addTestTask(t, s, "Task", model.PriorityMedium, nil)
	os.Remove(s.LocksPath())

	r := RunCheck(s)

	c := findCheck(r, "locks.yaml")
	if c == nil {
		t.Fatal("expected locks.yaml check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for missing locks.yaml, got %s", c.Status)
	}
}

func TestRunCheck_SelfDependency(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Self dep", Status: model.StatusPending, Priority: model.PriorityMedium, DependsOn: []string{"TASK-001"}},
	})

	r := RunCheck(s)

	c := findCheck(r, "task-no-self-dependency")
	if c == nil {
		t.Fatal("expected task-no-self-dependency check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for self-dependency, got %s", c.Status)
	}
	if !r.HasFailures() {
		t.Error("expected HasFailures=true for self-dependency")
	}
}

func TestRunCheck_InvalidTaskIDFormat(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "BAD-ID", Title: "Bad ID", Status: model.StatusPending, Priority: model.PriorityMedium},
	})

	r := RunCheck(s)

	c := findCheck(r, "task-id-format")
	if c == nil {
		t.Fatal("expected task-id-format check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for invalid task ID format, got %s", c.Status)
	}
}

func TestRunCheck_InvalidTestResult(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Bad result", Status: model.StatusPending, Priority: model.PriorityMedium, Test: model.TaskTest{Result: "not-a-real-result"}},
	})

	r := RunCheck(s)

	c := findCheck(r, "task-test-result-valid")
	if c == nil {
		t.Fatal("expected task-test-result-valid check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for invalid test result, got %s", c.Status)
	}
}

func TestRunCheck_DoneTaskNoFiles(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Done no files", Status: model.StatusDone, Priority: model.PriorityMedium, CompletedAt: "2025-01-01T00:00:00Z", Files: []string{}},
	})

	r := RunCheck(s)

	c := findCheck(r, "done-tasks-files")
	if c == nil {
		t.Fatal("expected done-tasks-files check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for done task with no files, got %s", c.Status)
	}
}

func TestRunCheck_DoneTaskUnknownResult(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Done unknown", Status: model.StatusDone, Priority: model.PriorityMedium, CompletedAt: "2025-01-01T00:00:00Z", Files: []string{"main.go"}},
	})

	r := RunCheck(s)

	c := findCheck(r, "done-tasks-test-result")
	if c == nil {
		t.Fatal("expected done-tasks-test-result check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for done task with unknown result, got %s", c.Status)
	}
}

func TestRunCheck_DoneTaskNoEvidence(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Done no ev", Status: model.StatusDone, Priority: model.PriorityMedium, CompletedAt: "2025-01-01T00:00:00Z", Files: []string{"main.go"}, Test: model.TaskTest{Result: model.TestResultPassed}},
	})

	r := RunCheck(s)

	c := findCheck(r, "done-tasks-evidence")
	if c == nil {
		t.Fatal("expected done-tasks-evidence check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for done task with no evidence, got %s", c.Status)
	}
}

func TestRunCheck_BlockedTaskNoReason(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Blocked no reason", Status: model.StatusBlocked, Priority: model.PriorityMedium},
	})

	r := RunCheck(s)

	c := findCheck(r, "blocked-tasks-reason")
	if c == nil {
		t.Fatal("expected blocked-tasks-reason check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for blocked task with no reason, got %s", c.Status)
	}
}

func TestRunCheck_MultipleInProgress(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "In progress A", Status: model.StatusInProgress, Priority: model.PriorityMedium},
		{ID: "TASK-002", Title: "In progress B", Status: model.StatusInProgress, Priority: model.PriorityMedium},
	})

	r := RunCheck(s)

	c := findCheck(r, "multiple-in-progress")
	if c == nil {
		t.Fatal("expected multiple-in-progress check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for multiple in_progress tasks, got %s", c.Status)
	}
}

func TestRunCheck_MultipleActiveLocks(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Locked task", Status: model.StatusInProgress, Priority: model.PriorityMedium},
	})
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)
	writeRawLocks(t, s, []model.Lock{
		{TaskID: "TASK-001", Agent: "agent1", Role: "dev", AcquiredAt: now, ExpiresAt: future, HeartbeatAt: now},
		{TaskID: "TASK-001", Agent: "agent2", Role: "dev", AcquiredAt: now, ExpiresAt: future, HeartbeatAt: now},
	})

	r := RunCheck(s)

	c := findCheck(r, "locks-duplicate")
	if c == nil {
		t.Fatal("expected locks-duplicate check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for duplicate active locks, got %s", c.Status)
	}
}

func TestRunCheck_DoneTaskWithActiveLock(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Done locked", Status: model.StatusDone, Priority: model.PriorityMedium, CompletedAt: "2025-01-01T00:00:00Z", Files: []string{"main.go"}},
	})
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)
	writeRawLocks(t, s, []model.Lock{
		{TaskID: "TASK-001", Agent: "agent1", Role: "dev", AcquiredAt: now, ExpiresAt: future, HeartbeatAt: now},
	})

	r := RunCheck(s)

	c := findCheck(r, "locks-done-task")
	if c == nil {
		t.Fatal("expected locks-done-task check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for done task with active lock, got %s", c.Status)
	}
}

func TestRunCheck_HasWarnings(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Blocked no reason", Status: model.StatusBlocked, Priority: model.PriorityMedium},
	})

	r := RunCheck(s)

	if !r.HasWarnings() {
		t.Error("expected HasWarnings=true for blocked task with no reason")
	}
	if r.HasFailures() {
		t.Error("expected HasFailures=false (no failures, only warnings)")
	}
}

func TestRunCheck_EvidenceDirCheck(t *testing.T) {
	s, _ := setupTestStore(t)
	addTestTask(t, s, "Task", model.PriorityMedium, nil)

	r := RunCheck(s)

	c := findCheck(r, "evidence-dir")
	if c == nil {
		t.Fatal("expected evidence-dir check")
	}
	if c.Status != CheckPass {
		t.Errorf("expected pass for evidence-dir, got %s - %s", c.Status, c.Message)
	}
}

func TestRunCheck_HasFailuresAndPassedFlag(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Bad", Status: "invalid", Priority: model.PriorityMedium},
	})

	r := RunCheck(s)

	if !r.HasFailures() {
		t.Error("expected HasFailures=true")
	}
	if r.Passed {
		t.Error("expected Passed=false with failures")
	}
}
func TestRunCheck_DoneTaskMissingDependency(t *testing.T) {
	s, _ := setupTestStore(t)
	writeRawTasks(t, s, []model.Task{
		{ID: "TASK-001", Title: "Done missing dep", Status: model.StatusDone, Priority: model.PriorityMedium, CompletedAt: "2025-01-01T00:00:00Z", DependsOn: []string{"TASK-999"}},
	})

	r := RunCheck(s)

	c := findCheck(r, "done-task-dependencies")
	if c == nil {
		t.Fatal("expected done-task-dependencies check")
	}
	if c.Status != CheckFail {
		t.Errorf("expected fail for done task with missing dependency, got %s", c.Status)
	}
	if !r.HasFailures() {
		t.Error("expected HasFailures=true for done task with missing dependency")
	}
}

func TestRunCheck_GitNotRepo(t *testing.T) {
	s, _ := setupTestStore(t)
	addTestTask(t, s, "Task", model.PriorityMedium, nil)

	r := RunCheck(s)

	c := findCheck(r, "git-repo")
	if c == nil {
		t.Fatal("expected git-repo check")
	}
	if c.Status != CheckInfo {
		t.Errorf("expected info for non-git repo, got %s", c.Status)
	}
}

func TestRunCheck_GitChangedFilesNotBound(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	s, _ := setupTestStore(t)
	addTestTask(t, s, "Task", model.PriorityMedium, nil)

	// Init git repo at the project root (parent of .ctask)
	root := filepath.Dir(s.BasePath)
	runGitQuiet(t, root, "init")
	runGitQuiet(t, root, "add", ".")
	runGitQuiet(t, root, "-c", "user.name=Test", "-c", "user.email=test@test", "commit", "-m", "init")

	// Create an unbound changed file
	unbound := filepath.Join(root, "unbound.txt")
	if err := os.WriteFile(unbound, []byte("change\n"), 0644); err != nil {
		t.Fatalf("failed to write unbound file: %v", err)
	}

	r := RunCheck(s)

	c := findCheck(r, "git-unbound-changes")
	if c == nil {
		t.Fatal("expected git-unbound-changes check")
	}
	if c.Status != CheckWarn {
		t.Errorf("expected warn for unbound changes, got %s", c.Status)
	}
}

func TestRunCheck_GitChangedFilesInCtaskDir(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	s, _ := setupTestStore(t)
	addTestTask(t, s, "Task", model.PriorityMedium, nil)

	root := filepath.Dir(s.BasePath)
	runGitQuiet(t, root, "init")
	runGitQuiet(t, root, "add", ".")
	runGitQuiet(t, root, "-c", "user.name=Test", "-c", "user.email=test@test", "commit", "-m", "init")

	// Create a change inside .ctask/ (should be filtered out)
	ctaskFile := filepath.Join(s.BasePath, "evidence", "test.md")
	if err := os.MkdirAll(filepath.Dir(ctaskFile), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(ctaskFile, []byte("test\n"), 0644); err != nil {
		t.Fatalf("failed to write ctask file: %v", err)
	}

	r := RunCheck(s)

	c := findCheck(r, "git-unbound-changes")
	if c == nil {
		t.Fatal("expected git-unbound-changes check")
	}
	if c.Status != CheckPass {
		t.Errorf("expected pass for .ctask-only changes, got %s - %s", c.Status, c.Message)
	}
}
