package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
)

// TestCompleteTask_AutoFilesMergesWithExplicitFiles verifies that --files and
// --auto-files are merged with deduplication.
func TestCompleteTask_AutoFilesMergesWithExplicitFiles(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	s, dir := setupTestStore(t)
	runGitQuiet(t, dir, "init")
	runGitQuiet(t, dir, "-c", "user.name=Test", "-c", "user.email=test@codeledger.local", "commit", "--allow-empty", "-m", "init")

	feature := filepath.Join(dir, "feature.go")
	os.WriteFile(feature, []byte("package main\n"), 0644)

	task := addTestTask(t, s, "Merge files task", model.PriorityHigh, nil)
	if err := CompleteTask(s, task.ID, "feature.go,other.go", "", model.TestResultPassed, "", true, false); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	updated, err := GetTaskByID(s, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}

	expected := map[string]bool{"feature.go": true, "other.go": true}
	for _, f := range updated.Files {
		delete(expected, f)
	}
	if len(expected) > 0 {
		t.Errorf("expected files not found: %v (got %v)", expected, updated.Files)
	}

	seen := make(map[string]int)
	for _, f := range updated.Files {
		seen[f]++
	}
	for f, count := range seen {
		if count > 1 {
			t.Errorf("file %s appeared %d times (should be deduped)", f, count)
		}
	}
}

// TestCompleteTask_CaptureDiff_CreatesDiffFile verifies that --capture-diff
// creates a separate .diff file and adds it to Task.Evidence.
func TestCompleteTask_CaptureDiff_CreatesDiffFile(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	s, dir := setupTestStore(t)
	runGitQuiet(t, dir, "init")
	feature := filepath.Join(dir, "feature.go")
	os.WriteFile(feature, []byte("old\n"), 0644)
	runGitQuiet(t, dir, "add", "feature.go")
	runGitQuiet(t, dir, "-c", "user.name=Test", "-c", "user.email=test@codeledger.local", "commit", "-m", "init")
	os.WriteFile(feature, []byte("new content\n"), 0644)

	task := addTestTask(t, s, "Capture diff task", model.PriorityHigh, nil)
	if err := CompleteTask(s, task.ID, "", "", model.TestResultPassed, "", false, true); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	diffPath := s.EvidenceDiffPath(task.ID)
	if _, err := os.Stat(diffPath); err != nil {
		t.Errorf("expected .diff file at %s: %v", diffPath, err)
	}

	data, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("failed to read .diff file: %v", err)
	}
	if !strings.Contains(string(data), "feature.go") {
		t.Errorf("expected .diff to contain feature.go, got:\n%s", data)
	}

	updated, err := GetTaskByID(s, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	foundMd, foundDiff := false, false
	for _, e := range updated.Evidence {
		if strings.HasSuffix(e, ".md") {
			foundMd = true
		}
		if strings.HasSuffix(e, ".diff") {
			foundDiff = true
		}
	}
	if !foundMd {
		t.Error("expected .md path in Task.Evidence")
	}
	if !foundDiff {
		t.Error("expected .diff path in Task.Evidence")
	}

	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	assertEventType(t, events, model.EventDiffCaptured)
}

// TestCompleteTask_CaptureDiff_EmptyDiff verifies that --capture-diff creates
// a .diff file with "No git diff captured." when there are no changes.
func TestCompleteTask_CaptureDiff_EmptyDiff(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	s, dir := setupTestStore(t)
	runGitQuiet(t, dir, "init")
	runGitQuiet(t, dir, "-c", "user.name=Test", "-c", "user.email=test@codeledger.local", "commit", "--allow-empty", "-m", "init")

	task := addTestTask(t, s, "Empty diff task", model.PriorityHigh, nil)
	if err := CompleteTask(s, task.ID, "", "", model.TestResultPassed, "", false, true); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	data, err := os.ReadFile(s.EvidenceDiffPath(task.ID))
	if err != nil {
		t.Fatalf("failed to read .diff file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "No git diff captured." {
		t.Errorf("expected 'No git diff captured.', got %q", data)
	}
}

// TestCompleteTask_EvidenceMdDoesNotContainFullDiff verifies the .md evidence
// file does not contain the full diff block.
func TestCompleteTask_EvidenceMdDoesNotContainFullDiff(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	s, dir := setupTestStore(t)
	runGitQuiet(t, dir, "init")
	feature := filepath.Join(dir, "feature.go")
	os.WriteFile(feature, []byte("old\n"), 0644)
	runGitQuiet(t, dir, "add", "feature.go")
	runGitQuiet(t, dir, "-c", "user.name=Test", "-c", "user.email=test@codeledger.local", "commit", "-m", "init")
	os.WriteFile(feature, []byte("new\n"), 0644)

	task := addTestTask(t, s, "No full diff in md task", model.PriorityHigh, nil)
	if err := CompleteTask(s, task.ID, "", "", model.TestResultPassed, "", false, true); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	mdData, err := os.ReadFile(s.EvidencePath(task.ID))
	if err != nil {
		t.Fatalf("failed to read .md evidence: %v", err)
	}
	if strings.Contains(string(mdData), "```diff") {
		t.Errorf("expected .md evidence to NOT contain full diff block")
	}
}

// TestAddEvidence_AppendsToMarkdownFile verifies that AddEvidence appends
// content to the .md evidence file (not overwrites).
func TestAddEvidence_AppendsToMarkdownFile(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Add evidence task", model.PriorityMedium, nil)

	if err := AddEvidence(s, task.ID, "test", "go test ./... passed"); err != nil {
		t.Fatalf("AddEvidence failed: %v", err)
	}

	data, err := os.ReadFile(s.EvidencePath(task.ID))
	if err != nil {
		t.Fatalf("failed to read evidence file: %v", err)
	}
	if !strings.Contains(string(data), "go test ./... passed") {
		t.Errorf("expected evidence to contain test result, got:\n%s", data)
	}

	if err := AddEvidence(s, task.ID, "review", "Code review passed"); err != nil {
		t.Fatalf("AddEvidence second call failed: %v", err)
	}
	data, _ = os.ReadFile(s.EvidencePath(task.ID))
	if !strings.Contains(string(data), "go test ./... passed") {
		t.Errorf("expected first evidence to persist, got:\n%s", data)
	}
	if !strings.Contains(string(data), "Code review passed") {
		t.Errorf("expected second evidence appended, got:\n%s", data)
	}
}

// TestAddEvidence_AddsPathToTaskEvidence verifies the .md path is added to
// Task.Evidence without duplication.
func TestAddEvidence_AddsPathToTaskEvidence(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Evidence path task", model.PriorityMedium, nil)

	if err := AddEvidence(s, task.ID, "manual", "Manual verification"); err != nil {
		t.Fatalf("AddEvidence failed: %v", err)
	}

	updated, _ := GetTaskByID(s, task.ID)
	relPath := s.EvidenceRelPath(task.ID)
	found := false
	for _, e := range updated.Evidence {
		if e == relPath {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %s in Task.Evidence, got %v", relPath, updated.Evidence)
	}

	AddEvidence(s, task.ID, "manual", "Another check")
	updated, _ = GetTaskByID(s, task.ID)
	count := 0
	for _, e := range updated.Evidence {
		if e == relPath {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 entry for %s, got %d", relPath, count)
	}
}

// TestAddEvidence_LogsEvent verifies that an evidence.added event is logged.
func TestAddEvidence_LogsEvent(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Event evidence task", model.PriorityMedium, nil)

	AddEvidence(s, task.ID, "test", "tests passed")

	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	assertEventType(t, events, model.EventEvidenceAdded)
}

// TestCompleteTask_LogsFilesAttachedEvent verifies task.files_attached is logged.
func TestCompleteTask_LogsFilesAttachedEvent(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Files attached task", model.PriorityHigh, nil)

	CompleteTask(s, task.ID, "main.go,util.go", "", model.TestResultPassed, "", false, false)

	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	assertEventType(t, events, model.EventFilesAttached)
}

// TestCompleteTask_AutoFilesNotGitRepo verifies --auto-files gives a friendly
// error when not in a git repo.
func TestCompleteTask_AutoFilesNotGitRepo(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Non-git auto-files", model.PriorityHigh, nil)

	err := CompleteTask(s, task.ID, "", "", model.TestResultPassed, "", true, false)
	if err == nil {
		t.Fatal("expected error for --auto-files in non-git repo")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("expected error mentioning git repository, got: %v", err)
	}
}

// TestCompleteTask_CaptureDiffNotGitRepo verifies --capture-diff gives a
// friendly error when not in a git repo.
func TestCompleteTask_CaptureDiffNotGitRepo(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Non-git capture-diff", model.PriorityHigh, nil)

	err := CompleteTask(s, task.ID, "", "", model.TestResultPassed, "", false, true)
	if err == nil {
		t.Fatal("expected error for --capture-diff in non-git repo")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("expected error mentioning git repository, got: %v", err)
	}
}
