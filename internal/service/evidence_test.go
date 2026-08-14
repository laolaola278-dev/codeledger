package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/model"
)

func TestCompleteTaskAutoReleasesLockAndRecordsEvidence(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Done lock task", model.PriorityHigh, nil)

	if _, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, "agent1", "developer", "120m"); err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}
	if err := CompleteTask(s, clock.RealClock{}, task.ID, CompleteOptions{
		Files:  "main.go",
		Test:   "go test ./...",
		Result: model.TestResultPassed,
		Note:   "completed",
		Agent:  "agent1",
	}); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	updated, err := GetTaskByID(s, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if updated.Status != model.StatusDone {
		t.Errorf("expected status done, got %s", updated.Status)
	}

	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	for _, l := range locks.Locks {
		if l.TaskID == task.ID {
			t.Errorf("expected lock for %s to be released on done", task.ID)
		}
	}

	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	assertEventType(t, events, model.EventTaskCompleted)
	assertEventType(t, events, model.EventEvidenceRecorded)
	assertEventType(t, events, model.EventLockReleasedOnDone)

	if _, err := os.Stat(s.EvidencePath(task.ID)); err != nil {
		t.Errorf("expected evidence file to exist: %v", err)
	}
}

func TestCompleteTaskRecordsEvidenceFile(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Evidence task", model.PriorityMedium, nil)

	if err := CompleteTask(s, clock.RealClock{}, task.ID, CompleteOptions{
		Files:  "main.go,main_test.go",
		Test:   "go test ./...",
		Result: model.TestResultPassed,
		Note:   "finished",
	}); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	content, err := os.ReadFile(s.EvidencePath(task.ID))
	if err != nil {
		t.Fatalf("failed to read evidence file: %v", err)
	}
	for _, want := range []string{
		"# Evidence: " + task.ID,
		"main.go",
		"main_test.go",
		"go test ./...",
		"passed",
		"finished",
		"evidence/" + task.ID + ".md",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("expected evidence to contain %q, got:\n%s", want, content)
		}
	}
}

func TestCompleteTaskAutoDetectsGitChangedFiles(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	s, dir := setupTestStore(t)
	runGitQuiet(t, dir, "init")

	feature := filepath.Join(dir, "feature.txt")
	if err := os.WriteFile(feature, []byte("one\n"), 0644); err != nil {
		t.Fatalf("failed to write feature file: %v", err)
	}
	runGitQuiet(t, dir, "add", "feature.txt")
	runGitQuiet(t, dir, "-c", "user.name=CodeLedger Test", "-c", "user.email=test@codeledger.local", "commit", "-m", "init")

	if err := os.WriteFile(feature, []byte("two\n"), 0644); err != nil {
		t.Fatalf("failed to modify feature file: %v", err)
	}

	task := addTestTask(t, s, "Git task", model.PriorityHigh, nil)
	if err := CompleteTask(s, clock.RealClock{}, task.ID, CompleteOptions{Result: model.TestResultPassed, AutoFiles: true}); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	updated, err := GetTaskByID(s, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	found := false
	for _, f := range updated.Files {
		if f == "feature.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected feature.txt in task.Files, got %v", updated.Files)
	}
}

func assertEventType(t *testing.T, events []model.Event, want string) {
	t.Helper()
	for _, evt := range events {
		if evt.Type == want {
			return
		}
	}
	t.Errorf("expected event type %q in events", want)
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func runGitQuiet(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
