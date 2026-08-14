package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// setupTestStore creates a temporary directory and initializes a store.
func setupTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "codeledger-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	s := store.NewStore(dir)
	if err := InitProject(s); err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}
	return s, dir
}

// addTestTask is a helper to quickly add a task for testing.
func addTestTask(t *testing.T, s *store.Store, title, priority string, dependsOn []string) *model.Task {
	t.Helper()
	task, err := AddTask(s, title, "", priority, dependsOn)
	if err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	return task
}

// TestNextTask_PriorityHighBeforeMedium verifies that high priority tasks
// are suggested before medium priority tasks.
func TestNextTask_PriorityHighBeforeMedium(t *testing.T) {
	s, _ := setupTestStore(t)

	addTestTask(t, s, "Medium priority task", model.PriorityMedium, nil)
	addTestTask(t, s, "High priority task", model.PriorityHigh, nil)

	result, err := NextTask(s, "", clock.RealClock{})
	if err != nil {
		t.Fatalf("NextTask failed: %v", err)
	}
	if !result.Available {
		t.Fatal("expected an available task")
	}
	if result.Task.Title != "High priority task" {
		t.Errorf("expected high priority task first, got %s", result.Task.Title)
	}
}

// TestNextTask_DependsOnNotDone verifies that a task with unmet dependencies
// is not returned by NextTask.
func TestNextTask_DependsOnNotDone(t *testing.T) {
	s, _ := setupTestStore(t)

	taskA := addTestTask(t, s, "Task A", model.PriorityHigh, nil)
	addTestTask(t, s, "Task B", model.PriorityMedium, []string{taskA.ID})

	// Task B depends on Task A, which is not done yet.
	// Only Task A should be available.
	result, err := NextTask(s, "", clock.RealClock{})
	if err != nil {
		t.Fatalf("NextTask failed: %v", err)
	}
	if !result.Available {
		t.Fatal("expected an available task")
	}
	if result.Task.ID != taskA.ID {
		t.Errorf("expected task %s, got %s", taskA.ID, result.Task.ID)
	}
}

// TestNextTask_LockedTaskNotReturned verifies that a task with an active lock
// is not returned by NextTask.
func TestNextTask_LockedTaskNotReturned(t *testing.T) {
	s, _ := setupTestStore(t)

	taskA := addTestTask(t, s, "Task A", model.PriorityHigh, nil)
	addTestTask(t, s, "Task B", model.PriorityMedium, nil)

	// Manually add a lock for Task A. Legacy pre-lease locks (no lease_id /
	// lease_duration) that have not expired also block the task fail-closed.
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	lock := model.Lock{
		TaskID:      taskA.ID,
		Agent:       "test-agent",
		Role:        "developer",
		AcquiredAt:  time.Now().Format(time.RFC3339),
		ExpiresAt:   future,
		HeartbeatAt: time.Now().Format(time.RFC3339),
	}
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	locks.Locks = append(locks.Locks, lock)
	if err := s.WriteLocks(locks); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}

	// Task A is locked, so Task B should be next
	result, err := NextTask(s, "", clock.RealClock{})
	if err != nil {
		t.Fatalf("NextTask failed: %v", err)
	}
	if !result.Available {
		t.Fatal("expected an available task")
	}
	if result.Task.ID != "TASK-002" {
		t.Errorf("expected TASK-002 (Task B), got %s", result.Task.ID)
	}
}

// TestClaimTask_SetsInProgress verifies that claiming a task changes its
// status from pending to in_progress and records a lease with a lease_id.
func TestClaimTask_SetsInProgress(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "Claimable task", model.PriorityHigh, nil)

	lock, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "test-agent"}, "developer", "120m")
	if err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}
	if lock.LeaseID == "" {
		t.Error("expected a lease_id on the claimed lock")
	}

	// Verify task status is in_progress
	updated, err := GetTaskByID(s, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if updated.Status != model.StatusInProgress {
		t.Errorf("expected status in_progress, got %s", updated.Status)
	}

	// Verify lock was created
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	found := false
	for _, l := range locks.Locks {
		if l.TaskID == task.ID && l.Agent == "test-agent" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected lock to be created for the task")
	}
}

// TestReleaseTask_ResetsToPending verifies that releasing a task changes its
// status from in_progress back to pending.
func TestReleaseTask_ResetsToPending(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "Releasable task", model.PriorityHigh, nil)

	lock, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "test-agent"}, "developer", "120m")
	if err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	if err := ReleaseTask(s, clock.RealClock{}, task.ID, lease.Auth{Agent: "test-agent", LeaseID: lock.LeaseID}); err != nil {
		t.Fatalf("ReleaseTask failed: %v", err)
	}

	// Verify task status is back to pending
	updated, err := GetTaskByID(s, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if updated.Status != model.StatusPending {
		t.Errorf("expected status pending after release, got %s", updated.Status)
	}

	// Verify lock was removed
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	for _, l := range locks.Locks {
		if l.TaskID == task.ID {
			t.Error("expected lock to be removed after release")
		}
	}
}

// TestClaimTask_ExpiredLockDoesNotBlock verifies that an expired NEW-format
// lock does not prevent same-task re-claiming (the entry is replaced with a
// fresh lease).
func TestClaimTask_ExpiredLockDoesNotBlock(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "Expired lock task", model.PriorityHigh, nil)

	// Add an expired, well-formed (non-legacy) lock.
	now := time.Now().UTC()
	expiredLock := model.Lock{
		TaskID:        task.ID,
		Agent:         "old-agent",
		Role:          "developer",
		LeaseID:       "lease-old",
		LeaseDuration: "120m",
		AcquiredAt:    now.Add(-3 * time.Hour).Format(time.RFC3339),
		ExpiresAt:     now.Add(-2 * time.Hour).Format(time.RFC3339),
		HeartbeatAt:   now.Add(-3 * time.Hour).Format(time.RFC3339),
	}
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	locks.Locks = append(locks.Locks, expiredLock)
	if err := s.WriteLocks(locks); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}

	// Claim should succeed despite the expired lock
	if _, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "new-agent"}, "developer", "120m"); err != nil {
		t.Fatalf("ClaimTask should succeed with expired lock, but got: %v", err)
	}

	// Verify new lock is in place
	locks, err = s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	found := false
	for _, l := range locks.Locks {
		if l.TaskID == task.ID && l.Agent == "new-agent" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected new lock to be created after claiming with expired lock")
	}
}

// TestClaimTask_ExpiredLegacyLockFailClosed verifies that a legacy entry that
// has ALSO expired still blocks a claim fail-closed (legacy precedence over
// expired): it must be taken over with --force --reason --agent, never
// silently reclaimed.
func TestClaimTask_ExpiredLegacyLockFailClosed(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Expired legacy task", model.PriorityHigh, nil)

	now := time.Now().UTC()
	expiredLegacy := model.Lock{
		TaskID:     task.ID,
		Agent:      "old-agent",
		Role:       "developer",
		AcquiredAt: now.Add(-3 * time.Hour).Format(time.RFC3339),
		ExpiresAt:  now.Add(-2 * time.Hour).Format(time.RFC3339),
	}
	writeRawLock(t, s, expiredLegacy)

	if _, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "new-agent"}, "developer", "120m"); !errors.Is(err, ErrLegacyLease) {
		t.Fatalf("expected ErrLegacyLease for expired legacy claim, got: %v", err)
	}
}

// TestClaimTask_AlreadyLocked verifies that claiming a task with an active
// lease returns a lease conflict error.
func TestClaimTask_AlreadyLocked(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "Locked task", model.PriorityHigh, nil)

	// First claim succeeds
	if _, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "agent1"}, "developer", "120m"); err != nil {
		t.Fatalf("first ClaimTask failed: %v", err)
	}

	// Second claim should fail
	_, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "agent2"}, "developer", "120m")
	if err == nil {
		t.Fatal("expected error when claiming an already locked task")
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Errorf("expected ErrLeaseConflict, got: %v", err)
	}
}

// TestNextTask_NoAvailableTasks verifies NextTask returns a clear message
// when no tasks are available.
func TestNextTask_NoAvailableTasks(t *testing.T) {
	s, _ := setupTestStore(t)

	result, err := NextTask(s, "", clock.RealClock{})
	if err != nil {
		t.Fatalf("NextTask failed: %v", err)
	}
	if result.Available {
		t.Fatal("expected no available tasks")
	}
	if result.Message == "" {
		t.Error("expected a message when no tasks are available")
	}
}

// TestHeartbeatTask verifies that sending a heartbeat updates the heartbeat_at field.
func TestHeartbeatTask(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "Heartbeat task", model.PriorityHigh, nil)

	lock, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "test-agent"}, "developer", "120m")
	if err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	// Small delay to ensure timestamp changes
	time.Sleep(1100 * time.Millisecond)

	if _, err := HeartbeatTask(s, clock.RealClock{}, task.ID, lease.Auth{Agent: "test-agent", LeaseID: lock.LeaseID}); err != nil {
		t.Fatalf("HeartbeatTask failed: %v", err)
	}

	// Verify heartbeat_at was updated
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	for _, l := range locks.Locks {
		if l.TaskID == task.ID {
			if l.HeartbeatAt == l.AcquiredAt {
				t.Error("expected heartbeat_at to be updated after heartbeat")
			}
			break
		}
	}
}

// TestReleaseTask_NoLock returns error when releasing a task without a lock.
func TestReleaseTask_NoLock(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "No lock task", model.PriorityHigh, nil)

	err := ReleaseTask(s, clock.RealClock{}, task.ID, lease.Auth{})
	if err == nil {
		t.Fatal("expected error when releasing a task with no lock")
	}
	if !errors.Is(err, ErrLeaseNotFound) {
		t.Errorf("expected ErrLeaseNotFound, got: %v", err)
	}
}

// TestClaimTask_DoneTaskFails verifies claiming a done task returns an error.
func TestClaimTask_DoneTaskFails(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "Done task", model.PriorityHigh, nil)
	if err := CompleteTask(s, clock.RealClock{}, task.ID, CompleteOptions{Result: model.TestResultPassed}); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	_, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "test-agent"}, "developer", "120m")
	if err == nil {
		t.Fatal("expected error when claiming a done task")
	}
}

// TestClaimTask_BlockedTaskFails verifies claiming a blocked task returns an error.
func TestClaimTask_BlockedTaskFails(t *testing.T) {
	s, _ := setupTestStore(t)

	task := addTestTask(t, s, "Blocked task", model.PriorityHigh, nil)
	if err := BlockTask(s, clock.RealClock{}, task.ID, "blocked reason", lease.Auth{}); err != nil {
		t.Fatalf("BlockTask failed: %v", err)
	}

	_, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, task.ID, lease.Auth{Agent: "test-agent"}, "developer", "120m")
	if err == nil {
		t.Fatal("expected error when claiming a blocked task")
	}
}

// TestNextTask_PriorityOrdering verifies correct priority ordering with
// multiple tasks of different priorities.
func TestNextTask_PriorityOrdering(t *testing.T) {
	s, _ := setupTestStore(t)

	addTestTask(t, s, "Low priority task", model.PriorityLow, nil)
	addTestTask(t, s, "Medium priority task", model.PriorityMedium, nil)
	addTestTask(t, s, "High priority task", model.PriorityHigh, nil)

	// High priority should be first
	result, err := NextTask(s, "", clock.RealClock{})
	if err != nil {
		t.Fatalf("NextTask failed: %v", err)
	}
	if result.Task.Title != "High priority task" {
		t.Errorf("expected 'High priority task', got '%s'", result.Task.Title)
	}

	// Complete high priority, medium should be next
	if err := CompleteTask(s, clock.RealClock{}, result.Task.ID, CompleteOptions{Result: model.TestResultPassed}); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}
	result, err = NextTask(s, "", clock.RealClock{})
	if err != nil {
		t.Fatalf("NextTask failed: %v", err)
	}
	if result.Task.Title != "Medium priority task" {
		t.Errorf("expected 'Medium priority task', got '%s'", result.Task.Title)
	}
}

// TestNextTask_DependsOnChain verifies that a chain of dependencies is
// respected by NextTask.
func TestNextTask_DependsOnChain(t *testing.T) {
	s, _ := setupTestStore(t)

	taskA := addTestTask(t, s, "Task A", model.PriorityHigh, nil)
	taskB := addTestTask(t, s, "Task B", model.PriorityMedium, []string{taskA.ID})
	addTestTask(t, s, "Task C", model.PriorityLow, []string{taskB.ID})

	// Only Task A should be available
	result, err := NextTask(s, "", clock.RealClock{})
	if err != nil {
		t.Fatalf("NextTask failed: %v", err)
	}
	if result.Task.ID != taskA.ID {
		t.Errorf("expected Task A (%s), got %s", taskA.ID, result.Task.ID)
	}
}

// TestClaim_DoesNotCleanOtherTaskExpiredLock verifies that claiming one task
// does NOT remove another task's expired lock entry (no global expiry sweep).
func TestClaim_DoesNotCleanOtherTaskExpiredLock(t *testing.T) {
	s, _ := setupTestStore(t)

	taskA := addTestTask(t, s, "Task A", model.PriorityHigh, nil)
	taskB := addTestTask(t, s, "Task B", model.PriorityMedium, nil)

	// Add a well-formed (non-legacy) expired lock for Task B.
	now := time.Now().UTC()
	expiredLock := model.Lock{
		TaskID:        taskB.ID,
		Agent:         "old-agent",
		Role:          "developer",
		LeaseID:       "lease-old-b",
		LeaseDuration: "120m",
		AcquiredAt:    now.Add(-3 * time.Hour).Format(time.RFC3339),
		ExpiresAt:     now.Add(-2 * time.Hour).Format(time.RFC3339),
		HeartbeatAt:   now.Add(-3 * time.Hour).Format(time.RFC3339),
	}
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	locks.Locks = append(locks.Locks, expiredLock)
	if err := s.WriteLocks(locks); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}

	// Claim Task A: this must NOT clean Task B's expired lock.
	if _, err := ClaimTask(s, clock.RealClock{}, lease.RandomID, taskA.ID, lease.Auth{Agent: "agent1"}, "developer", "120m"); err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}

	locks, err = s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	foundB := false
	for _, l := range locks.Locks {
		if l.TaskID == taskB.ID {
			foundB = true
			if l.Agent != "old-agent" || l.LeaseID != "lease-old-b" {
				t.Errorf("task B expired entry was modified: %+v", l)
			}
		}
	}
	if !foundB {
		t.Error("claiming task A removed task B's expired lock (forbidden global sweep)")
	}
}

// TestCreateProjectDir ensures the test helper creates a proper directory structure.
func TestCreateProjectDir(t *testing.T) {
	s, dir := setupTestStore(t)

	// Verify the .ctask directory structure exists
	ctaskDir := filepath.Join(dir, store.DirName)
	info, err := os.Stat(ctaskDir)
	if err != nil {
		t.Fatalf(".ctask directory not found: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".ctask is not a directory")
	}

	// Verify locks.yaml exists
	if _, err := os.Stat(s.LocksPath()); err != nil {
		t.Fatalf("locks.yaml not found: %v", err)
	}
}
