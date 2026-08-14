package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// leaseFixture returns a deterministic time source and ID generator.
func leaseFixture(t0 time.Time) (clock.FixedClock, lease.IDGen) {
	return clock.FixedClock{T: t0}, lease.StaticID("lease-test-0001")
}

// writeRawLock writes exactly the given locks into locks.yaml, bypassing the
// service layer so tests can plant legacy/corrupt entries.
func writeRawLock(t *testing.T, s *store.Store, locks ...model.Lock) {
	t.Helper()
	if err := s.WriteLocks(&model.LockList{Locks: locks}); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}
}

// mustLease claims taskID and returns the created lease, failing the test on
// error.
func mustLease(t *testing.T, s *store.Store, clk clock.Clock, newID lease.IDGen, taskID, agent, ttl string) *model.Lock {
	t.Helper()
	lock, err := ClaimTask(s, clk, newID, taskID, agent, "developer", ttl)
	if err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}
	return lock
}

func TestClaimTask_RecordsLeaseFields(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Claim", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)

	lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	if lock.LeaseID != "lease-test-0001" {
		t.Errorf("expected injected lease_id, got %q", lock.LeaseID)
	}
	if lock.LeaseDuration != "30m" {
		t.Errorf("expected lease_duration 30m, got %q", lock.LeaseDuration)
	}
	wantExpiry := t0.Add(30 * time.Minute).Format(time.RFC3339)
	if lock.ExpiresAt != wantExpiry {
		t.Errorf("expected expiry %s, got %s", wantExpiry, lock.ExpiresAt)
	}
	if lock.HeartbeatAt != t0.Format(time.RFC3339) {
		t.Errorf("expected heartbeat_at %s, got %s", t0.Format(time.RFC3339), lock.HeartbeatAt)
	}
	if lock.AcquiredAt != t0.Format(time.RFC3339) {
		t.Errorf("expected acquired_at %s, got %s", t0.Format(time.RFC3339), lock.AcquiredAt)
	}
}

func TestHeartbeat_TrueRenewalExtendsByFullDuration(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Renew", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)

	lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")
	firstExpiry := lock.ExpiresAt

	// 10 minutes later, renew: the lease must be extended by the FULL 30m
	// recorded duration (now+30m), not merely stamped.
	clk.T = t0.Add(10 * time.Minute)
	renewed, err := HeartbeatTask(s, clk, task.ID, "agent1", "")
	if err != nil {
		t.Fatalf("HeartbeatTask failed: %v", err)
	}
	wantExpiry := t0.Add(10 * time.Minute).Add(30 * time.Minute).Format(time.RFC3339)
	if renewed.ExpiresAt != wantExpiry {
		t.Errorf("expected renewed expiry %s, got %s", wantExpiry, renewed.ExpiresAt)
	}
	if renewed.ExpiresAt == firstExpiry {
		t.Error("expected expiry to move forward after renewal")
	}
	if renewed.HeartbeatAt != t0.Add(10*time.Minute).Format(time.RFC3339) {
		t.Errorf("expected heartbeat_at to update, got %s", renewed.HeartbeatAt)
	}

	// The renewal is observable in the raw lock data: 1 minute past the
	// ORIGINAL expiry the stored expires_at is still in the future.
	clk.T = t0.Add(31 * time.Minute)
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	stored := locks.Locks[0].ExpiresAt
	if !strings.HasPrefix(stored, "2026-01-02T03:44:05") {
		t.Errorf("expected stored expiry to reflect the full-duration renewal, got %s", stored)
	}
	if locks.Locks[0].ExpiredAt(clk.Now()) {
		t.Error("stored expiry must still be in the future 1 minute past the original expiry")
	}

	// And after the renewed expiry (t0+10m+30m = t0+40m) the lease really
	// is expired: renewing again must fail with LEASE_EXPIRED.
	clk.T = t0.Add(41 * time.Minute)
	if _, err := HeartbeatTask(s, clk, task.ID, "agent1", ""); !errors.Is(err, ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired at renewed expiry, got: %v", err)
	}
}

func TestHeartbeat_StrictOwnerValidation(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Owner", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	// Wrong agent.
	if _, err := HeartbeatTask(s, clk, task.ID, "agent2", ""); !errors.Is(err, ErrLeaseConflict) {
		t.Errorf("expected ErrLeaseConflict for wrong agent, got: %v", err)
	}
	// No agent at all.
	if _, err := HeartbeatTask(s, clk, task.ID, "", ""); !errors.Is(err, ErrLeaseConflict) {
		t.Errorf("expected ErrLeaseConflict for missing agent, got: %v", err)
	}
}

func TestHeartbeat_WrongLeaseIDRejected(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "LeaseID", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	if _, err := HeartbeatTask(s, clk, task.ID, "agent1", "lease-wrong"); !errors.Is(err, ErrLeaseConflict) {
		t.Errorf("expected ErrLeaseConflict for wrong lease_id, got: %v", err)
	}
	// Correct lease id is accepted.
	if _, err := HeartbeatTask(s, clk, task.ID, "agent1", "lease-test-0001"); err != nil {
		t.Errorf("expected heartbeat with correct lease_id to succeed, got: %v", err)
	}
}

func TestHeartbeat_ExpiredLeaseCannotRenew(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Expired", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	clk.T = t0.Add(31 * time.Minute)
	if _, err := HeartbeatTask(s, clk, task.ID, "agent1", ""); !errors.Is(err, ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired, got: %v", err)
	}
}

func TestHeartbeat_LegacyLockFailClosed(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Legacy", model.PriorityHigh, nil)

	// Pre-lease lock: no lease_id.
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeRawLock(t, s, model.Lock{
		TaskID:     task.ID,
		Agent:      "old-agent",
		Role:       "developer",
		AcquiredAt: t0.Format(time.RFC3339),
		ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
	})

	if _, err := HeartbeatTask(s, clock.FixedClock{T: t0}, task.ID, "old-agent", ""); !errors.Is(err, ErrLegacyState) {
		t.Errorf("expected ErrLegacyState for legacy lock, got: %v", err)
	}
}

func TestRelease_StrictOwnerValidation(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Release owner", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	// Wrong agent without force.
	if err := ReleaseTask(s, clk, task.ID, "agent2", "", false, ""); !errors.Is(err, ErrLeaseConflict) {
		t.Errorf("expected ErrLeaseConflict for non-owner release, got: %v", err)
	}
	// Wrong agent with force but no reason.
	if err := ReleaseTask(s, clk, task.ID, "agent2", "", true, ""); !errors.Is(err, ErrForceRequired) {
		t.Errorf("expected ErrForceRequired for force without reason, got: %v", err)
	}
	// Wrong lease-id.
	if err := ReleaseTask(s, clk, task.ID, "agent1", "lease-wrong", false, ""); !errors.Is(err, ErrLeaseConflict) {
		t.Errorf("expected ErrLeaseConflict for wrong lease-id, got: %v", err)
	}
	// Owner succeeds and the lock is gone.
	if err := ReleaseTask(s, clk, task.ID, "agent1", "lease-test-0001", false, ""); err != nil {
		t.Fatalf("owner release failed: %v", err)
	}
	updated, err := GetTaskByID(s, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID failed: %v", err)
	}
	if updated.Status != model.StatusPending {
		t.Errorf("expected task back to pending after release, got %s", updated.Status)
	}
}

func TestRelease_ForceWithReasonBreaksOtherOwnersLease(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Broken", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	if err := ReleaseTask(s, clk, task.ID, "agent2", "", true, "agent1 went offline"); err != nil {
		t.Fatalf("forced release failed: %v", err)
	}

	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	for _, l := range locks.Locks {
		if l.TaskID == task.ID {
			t.Error("expected lease to be removed after forced release")
		}
	}

	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if !hasEvent(events, model.EventTaskLeaseBroken) {
		t.Errorf("expected task.lease_broken event, got %v", events)
	}
}

func TestRelease_LegacyLockFailClosed(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Legacy release", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeRawLock(t, s, model.Lock{
		TaskID:     task.ID,
		Agent:      "old-agent",
		Role:       "developer",
		AcquiredAt: t0.Format(time.RFC3339),
		ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
	})

	// Fail-closed: even the recorded owner cannot release without force.
	if err := ReleaseTask(s, clock.FixedClock{T: t0}, task.ID, "old-agent", "", false, ""); !errors.Is(err, ErrLegacyState) {
		t.Errorf("expected ErrLegacyState for legacy release, got: %v", err)
	}
	// Force + reason clears it.
	if err := ReleaseTask(s, clock.FixedClock{T: t0}, task.ID, "new-agent", "", true, "migrating legacy lock"); err != nil {
		t.Fatalf("legacy release with force+reason failed: %v", err)
	}
}

func TestRelease_ExpiredLeaseCleanableByAnyone(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Stale", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	// 31 minutes later the lease is expired: anyone can clean it without force.
	clk.T = t0.Add(31 * time.Minute)
	if err := ReleaseTask(s, clk, task.ID, "agent2", "", false, ""); err != nil {
		t.Fatalf("expired lease should be cleanable by anyone, got: %v", err)
	}
}

func TestRelease_NoLease_LeaseNotFound(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "No lease", model.PriorityHigh, nil)

	err := ReleaseTask(s, clock.RealClock{}, task.ID, "agent1", "", false, "")
	if !errors.Is(err, ErrLeaseNotFound) {
		t.Errorf("expected ErrLeaseNotFound, got: %v", err)
	}
}

func TestClaim_BlockedByActiveLease(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Double claim", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	_, err := ClaimTask(s, clk, newID, task.ID, "agent2", "developer", "30m")
	if !errors.Is(err, ErrLeaseConflict) {
		t.Errorf("expected ErrLeaseConflict for double claim, got: %v", err)
	}
}

func TestClaim_LegacyLockFailClosed(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Legacy claim", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeRawLock(t, s, model.Lock{
		TaskID:     task.ID,
		Agent:      "old-agent",
		Role:       "developer",
		AcquiredAt: t0.Format(time.RFC3339),
		ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
	})

	_, err := ClaimTask(s, clock.FixedClock{T: t0}, lease.StaticID("x"), task.ID, "agent2", "developer", "30m")
	if !errors.Is(err, ErrLegacyState) {
		t.Errorf("expected ErrLegacyState for claim over legacy lock, got: %v", err)
	}
}

func TestClaim_InvalidTTLFailsBeforeWrite(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Bad ttl", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)

	for _, ttl := range []string{"bogus", "0s", "-1m"} {
		_, err := ClaimTask(s, clk, newID, task.ID, "agent1", "developer", ttl)
		if !errors.Is(err, ErrInvalidTTL) {
			t.Errorf("ttl %q: expected ErrInvalidTTL, got: %v", ttl, err)
		}
	}

	// No lease or task write happened for the failed claims.
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	if len(locks.Locks) != 0 {
		t.Errorf("expected no lock written after invalid TTL, got %d", len(locks.Locks))
	}
}

func TestCompleteTask_LeaseGate(t *testing.T) {
	t.Run("requires owner", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Gate", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed})
		if !errors.Is(err, ErrLeaseConflict) {
			t.Errorf("expected ErrLeaseConflict without agent, got: %v", err)
		}
		// The task is untouched: still in_progress, lease still present.
		updated, _ := GetTaskByID(s, task.ID)
		if updated.Status != model.StatusInProgress {
			t.Errorf("expected task untouched after blocked completion, got %s", updated.Status)
		}
	})

	t.Run("owner completes and lease is released", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Owner done", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		if err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed, Agent: "agent1"}); err != nil {
			t.Fatalf("owner completion failed: %v", err)
		}
		locks, err := s.ReadLocks()
		if err != nil {
			t.Fatalf("ReadLocks failed: %v", err)
		}
		for _, l := range locks.Locks {
			if l.TaskID == task.ID {
				t.Error("expected lease released on done")
			}
		}
		events, _ := s.ReadEvents()
		if !hasEvent(events, model.EventLockReleasedOnDone) {
			t.Errorf("expected task.lock_released_on_done event, got %v", events)
		}
	})

	t.Run("force without reason rejected", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Force gate", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed, Agent: "agent2", Force: true})
		if !errors.Is(err, ErrForceRequired) {
			t.Errorf("expected ErrForceRequired, got: %v", err)
		}
	})

	t.Run("force with reason breaks lease", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Force done", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		if err := CompleteTask(s, clk, task.ID, CompleteOptions{
			Result: model.TestResultPassed, Agent: "agent2", Force: true, Reason: "agent1 gone",
		}); err != nil {
			t.Fatalf("forced completion failed: %v", err)
		}
		events, _ := s.ReadEvents()
		if !hasEvent(events, model.EventTaskLeaseBroken) {
			t.Errorf("expected task.lease_broken event, got %v", events)
		}
	})

	t.Run("legacy lock fail-closed then forced", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Legacy done", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		writeRawLock(t, s, model.Lock{
			TaskID:     task.ID,
			Agent:      "old-agent",
			Role:       "developer",
			AcquiredAt: t0.Format(time.RFC3339),
			ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
		})

		err := CompleteTask(s, clock.FixedClock{T: t0}, task.ID, CompleteOptions{Result: model.TestResultPassed, Agent: "old-agent"})
		if !errors.Is(err, ErrLegacyState) {
			t.Errorf("expected ErrLegacyState, got: %v", err)
		}
		if err := CompleteTask(s, clock.FixedClock{T: t0}, task.ID, CompleteOptions{
			Result: model.TestResultPassed, Agent: "new-agent", Force: true, Reason: "migration",
		}); err != nil {
			t.Fatalf("legacy forced completion failed: %v", err)
		}
	})

	t.Run("expired lease does not block completion", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Stale done", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		clk.T = t0.Add(31 * time.Minute)
		if err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed}); err != nil {
			t.Fatalf("completion over expired lease should succeed, got: %v", err)
		}
	})
}

func TestLock_LegacyDetection(t *testing.T) {
	valid := model.Lock{
		TaskID:        "TASK-001",
		Agent:         "agent1",
		Role:          "developer",
		LeaseID:       "lease-abc",
		LeaseDuration: "30m",
		AcquiredAt:    "2026-01-02T03:04:05Z",
		ExpiresAt:     "2026-01-02T03:34:05Z",
		HeartbeatAt:   "2026-01-02T03:04:05Z",
	}
	tests := []struct {
		name string
		mut  func(*model.Lock)
	}{
		{"valid lease is not legacy", func(l *model.Lock) {}},
		{"missing lease_id", func(l *model.Lock) { l.LeaseID = "" }},
		{"missing lease_duration", func(l *model.Lock) { l.LeaseDuration = "" }},
		{"bad lease_duration", func(l *model.Lock) { l.LeaseDuration = "bogus" }},
		{"bad acquired_at", func(l *model.Lock) { l.AcquiredAt = "not-a-time" }},
		{"bad expires_at", func(l *model.Lock) { l.ExpiresAt = "not-a-time" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := valid
			tt.mut(&l)
			legacy := l.Legacy()
			if tt.name == "valid lease is not legacy" && legacy {
				t.Error("expected valid lease to not be legacy")
			}
			if tt.name != "valid lease is not legacy" && !legacy {
				t.Error("expected legacy lock to be detected")
			}
		})
	}
}

func TestLock_ExpiredAt_MissingExpiryNotExpired(t *testing.T) {
	l := model.Lock{TaskID: "TASK-001", Agent: "a", ExpiresAt: ""}
	if l.ExpiredAt(time.Now()) {
		t.Error("missing expires_at must not be treated as expired (fail-closed handling applies instead)")
	}
}

// hasEvent reports whether any event has the given type (local helper to keep
// lease tests self-contained).
func hasEvent(events []model.Event, eventType string) bool {
	for _, e := range events {
		if e.Type == eventType {
			return true
		}
	}
	return false
}
