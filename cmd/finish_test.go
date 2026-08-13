package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
)

func TestCmd_Finish_NoTask(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "finish")
	if err != nil {
		t.Fatalf("finish failed: %v", err)
	}
	if !contains(out, "Session finished") {
		t.Errorf("expected session finished message, got: %s", out)
	}
	if !contains(out, "context.md updated") {
		t.Errorf("expected context.md updated message, got: %s", out)
	}
	if !contains(out, "Report saved") {
		t.Errorf("expected report saved message, got: %s", out)
	}
	if !contains(out, "TASK-001") {
		t.Errorf("expected next task TASK-001 in output, got: %s", out)
	}

	// Verify context.md was generated
	if _, err := os.Stat(filepath.Join(".ctask", "context.md")); err != nil {
		t.Errorf("context.md not generated: %v", err)
	}
}

func TestCmd_Finish_WithTask(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Implement auth")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	_, err = runRootArgs(t, "add", "Review auth", "--depends", "TASK-001")
	if err != nil {
		t.Fatalf("add 2 failed: %v", err)
	}
	_, err = runRootArgs(t, "claim", "TASK-001", "--agent", "codex")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Write a file so --auto-files can detect it
	if err := os.WriteFile("auth.go", []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to write auth.go: %v", err)
	}

	out, err := runRootArgs(t, "finish", "--task", "TASK-001", "--agent", "codex",
		"--files", "auth.go", "--test", "go test ./...", "--result", "passed",
		"--note", "done")
	if err != nil {
		t.Fatalf("finish --task failed: %v", err)
	}
	if !contains(out, "Completed task TASK-001") {
		t.Errorf("expected task completed message, got: %s", out)
	}

	// Verify task is done
	s := store.NewStore(".")
	task, err := service.GetTaskByID(s, "TASK-001")
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if task.Status != model.StatusDone {
		t.Errorf("expected task done, got %s", task.Status)
	}
	found := false
	for _, f := range task.Files {
		if f == "auth.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected auth.go in task.Files, got %v", task.Files)
	}

	// Verify lock released
	locks, _ := s.ReadLocks()
	for _, l := range locks.Locks {
		if l.TaskID == "TASK-001" {
			t.Error("expected lock for TASK-001 to be released")
		}
	}

	// Verify evidence exists
	if _, err := os.Stat(s.EvidencePath("TASK-001")); err != nil {
		t.Errorf("expected evidence file, got: %v", err)
	}

	// Verify next task is TASK-002
	if !contains(out, "TASK-002") {
		t.Errorf("expected next task TASK-002 in output, got: %s", out)
	}
}

func TestCmd_Finish_WithTaskNoResult(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Test task")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	_, err = runRootArgs(t, "claim", "TASK-001", "--agent", "codex")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Write a file so --auto-files could detect it (though we won't use it)
	if err := os.WriteFile("test.go", []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to write test.go: %v", err)
	}

	// Run finish --task TASK-001 WITHOUT --result
	out, err := runRootArgs(t, "finish", "--task", "TASK-001")
	// finish should not error — it prints a hint and continues
	if err != nil {
		t.Fatalf("finish --task (no result) failed: %v", err)
	}

	// Must contain the hint message
	if !contains(out, "not completed") && !contains(out, "--result is required") {
		t.Errorf("expected hint about --result being required, got: %s", out)
	}

	// Verify task is NOT done
	s := store.NewStore(".")
	task, err := service.GetTaskByID(s, "TASK-001")
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if task.Status == model.StatusDone {
		t.Error("expected task NOT to be done when --result is omitted")
	}
	if task.Status != model.StatusInProgress {
		t.Errorf("expected task to remain in_progress, got %s", task.Status)
	}

	// Verify lock is still active
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	hasLock := false
	for _, l := range locks.Locks {
		if l.TaskID == "TASK-001" && !l.IsExpired() {
			hasLock = true
			break
		}
	}
	if !hasLock {
		t.Error("expected lock for TASK-001 to remain active")
	}

	// Verify context and report are still generated (finish continues)
	if !contains(out, "context.md updated") {
		t.Errorf("expected context.md updated, got: %s", out)
	}
	if !contains(out, "Report saved") {
		t.Errorf("expected report saved, got: %s", out)
	}
}

func TestCmd_Finish_JSON(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "finish", "--json")
	if err != nil {
		t.Fatalf("finish --json failed: %v", err)
	}

	var result struct {
		Check          interface{} `json:"check"`
		TaskCompleted  string      `json:"task_completed"`
		ContextUpdated bool        `json:"context_updated"`
		ReportSaved    string      `json:"report_saved"`
		NextTask       string      `json:"next_task"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, out)
	}
	if result.Check == nil {
		t.Error("expected check in JSON output")
	}
	if !result.ContextUpdated {
		t.Error("expected context_updated=true")
	}
	if result.ReportSaved == "" {
		t.Error("expected report_saved to be non-empty")
	}
	if result.NextTask == "" {
		t.Error("expected next_task to be non-empty")
	}
}

func TestCmd_Finish_SkipContext(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "finish", "--skip-context")
	if err != nil {
		t.Fatalf("finish --skip-context failed: %v", err)
	}
	if !contains(out, "Skipped") {
		t.Errorf("expected skipped message, got: %s", out)
	}
}

func TestCmd_Finish_SkipReport(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "finish", "--skip-report")
	if err != nil {
		t.Fatalf("finish --skip-report failed: %v", err)
	}
	if !contains(out, "Skipped") {
		t.Errorf("expected skipped message, got: %s", out)
	}
}

func TestCmd_Finish_NotInitialized(t *testing.T) {
	dir, err := os.MkdirTemp("", "ctask-noinit-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	chdir(t, dir)

	_, err = runRootArgs(t, "finish")
	if err == nil {
		t.Error("expected error when project not initialized")
	}
}
