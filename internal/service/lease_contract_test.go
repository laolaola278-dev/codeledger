package service

import (
	"errors"
	"regexp"
	"strconv"
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

// readLockList returns the current locks.yaml contents.
func readLockList(t *testing.T, s *store.Store) *model.LockList {
	t.Helper()
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	return locks
}

// mustLease claims taskID and returns the created lease, failing the test on
// error.
func mustLease(t *testing.T, s *store.Store, clk clock.Clock, newID lease.IDGen, taskID, agent, ttl string) *model.Lock {
	t.Helper()
	lock, err := ClaimTask(s, clk, newID, taskID, lease.Auth{Agent: agent}, "developer", ttl)
	if err != nil {
		t.Fatalf("ClaimTask failed: %v", err)
	}
	return lock
}

// activeAuth returns the exact owner credentials for a held lease.
func activeAuth(lock *model.Lock) lease.Auth {
	return lease.Auth{Agent: lock.Agent, LeaseID: lock.LeaseID}
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
}

func TestHeartbeat_TrueRenewalExtendsByFullDuration(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Renew", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)

	lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")
	firstExpiry := lock.ExpiresAt

	// 10 minutes later, renew: the lease must be extended by the FULL 30m.
	clk.T = t0.Add(10 * time.Minute)
	renewed, err := HeartbeatTask(s, clk, task.ID, activeAuth(lock))
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

	// After the renewed expiry the lease really is expired.
	clk.T = t0.Add(41 * time.Minute)
	if _, err := HeartbeatTask(s, clk, task.ID, activeAuth(lock)); !errors.Is(err, ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired at renewed expiry, got: %v", err)
	}
}

func TestHeartbeat_ActiveExactCredentialMatrix(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Matrix", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	cases := []struct {
		name string
		auth lease.Auth
		want error
	}{
		{"exact owner succeeds", activeAuth(lock), nil},
		{"both missing", lease.Auth{}, ErrLeaseRequired},
		{"agent only", lease.Auth{Agent: "agent1"}, ErrLeaseRequired},
		{"lease-id only", lease.Auth{LeaseID: lock.LeaseID}, ErrLeaseRequired},
		{"wrong agent", lease.Auth{Agent: "agent2", LeaseID: lock.LeaseID}, ErrLeaseMismatch},
		{"wrong lease-id", lease.Auth{Agent: "agent1", LeaseID: "lease-wrong"}, ErrLeaseMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := HeartbeatTask(s, clk, task.ID, tc.auth)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("expected success, got: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got: %v", tc.want, err)
			}
		})
	}
}

func TestHeartbeat_ExpiredLeaseCannotRenew(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Expired", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	clk.T = t0.Add(31 * time.Minute)
	if _, err := HeartbeatTask(s, clk, task.ID, activeAuth(lock)); !errors.Is(err, ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired, got: %v", err)
	}
}

func TestHeartbeat_LegacyLockFailClosed(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Legacy", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeRawLock(t, s, model.Lock{
		TaskID:     task.ID,
		Agent:      "old-agent",
		Role:       "developer",
		AcquiredAt: t0.Format(time.RFC3339),
		ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
	})

	if _, err := HeartbeatTask(s, clock.FixedClock{T: t0}, task.ID, lease.Auth{Agent: "old-agent"}); !errors.Is(err, ErrLegacyLease) {
		t.Errorf("expected ErrLegacyLease for legacy lock, got: %v", err)
	}
}

func TestRelease_ActiveExactCredentialMatrix(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Release matrix", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	cases := []struct {
		name string
		auth lease.Auth
		want error
	}{
		{"both missing", lease.Auth{}, ErrLeaseRequired},
		{"agent only", lease.Auth{Agent: "agent1"}, ErrLeaseRequired},
		{"lease-id only", lease.Auth{LeaseID: lock.LeaseID}, ErrLeaseRequired},
		{"wrong agent", lease.Auth{Agent: "agent2", LeaseID: lock.LeaseID}, ErrLeaseMismatch},
		{"wrong lease-id", lease.Auth{Agent: "agent1", LeaseID: "lease-wrong"}, ErrLeaseMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ReleaseTask(s, clk, task.ID, tc.auth)
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got: %v", tc.want, err)
			}
			// Denied releases must not remove the lease.
			if !hasLockFor(readLockList(t, s), task.ID) {
				t.Error("denied release removed the lease")
			}
		})
	}

	// Exact owner succeeds and removes the lease.
	if err := ReleaseTask(s, clk, task.ID, activeAuth(lock)); err != nil {
		t.Fatalf("owner release failed: %v", err)
	}
	if hasLockFor(readLockList(t, s), task.ID) {
		t.Error("expected lease removed after owner release")
	}
}

func TestRelease_ForceWithReasonAndActorBreaksLease(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Broken", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	if err := ReleaseTask(s, clk, task.ID, lease.Auth{Agent: "agent2", Force: true, Reason: "agent1 went offline"}); err != nil {
		t.Fatalf("forced release failed: %v", err)
	}
	if hasLockFor(readLockList(t, s), task.ID) {
		t.Error("expected lease removed after forced release")
	}
	events, _ := s.ReadEvents()
	if !hasEvent(events, model.EventTaskLeaseBroken) {
		t.Errorf("expected task.lease_broken event")
	}
}

func TestRelease_ForceValidationMatrix(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Force validation", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	cases := []struct {
		name string
		auth lease.Auth
		want error
	}{
		{"missing reason", lease.Auth{Agent: "admin", Force: true}, ErrForceReasonRequired},
		{"missing actor", lease.Auth{Force: true, Reason: "x"}, ErrForceAgentRequired},
		{"both missing", lease.Auth{Force: true}, ErrForceReasonRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ReleaseTask(s, clk, task.ID, tc.auth)
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got: %v", tc.want, err)
			}
			if !hasLockFor(readLockList(t, s), task.ID) {
				t.Error("invalid force removed the lease")
			}
		})
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

	// Fail-closed: ordinary release (even with the recorded owner) is blocked.
	if err := ReleaseTask(s, clock.FixedClock{T: t0}, task.ID, lease.Auth{Agent: "old-agent"}); !errors.Is(err, ErrLegacyLease) {
		t.Errorf("expected ErrLegacyLease for legacy release, got: %v", err)
	}
	// Force + reason + actor clears it.
	if err := ReleaseTask(s, clock.FixedClock{T: t0}, task.ID, lease.Auth{Agent: "new-agent", Force: true, Reason: "migrating legacy lock"}); err != nil {
		t.Fatalf("legacy release with force+reason+agent failed: %v", err)
	}
}

func TestRelease_ExpiredLeaseFailClosed(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Stale", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	// 31 minutes later the lease is expired: ordinary release must fail closed
	// and the entry must be preserved (no silent cleanup).
	clk.T = t0.Add(31 * time.Minute)
	if err := ReleaseTask(s, clk, task.ID, activeAuth(lock)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expected ErrLeaseExpired for expired release, got: %v", err)
	}
	if !hasLockFor(readLockList(t, s), task.ID) {
		t.Error("expired release removed the entry; it must be preserved")
	}
}

func TestRelease_NoLease_LeaseNotFound(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "No lease", model.PriorityHigh, nil)

	err := ReleaseTask(s, clock.RealClock{}, task.ID, lease.Auth{Agent: "agent1"})
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

	_, err := ClaimTask(s, clk, newID, task.ID, lease.Auth{Agent: "agent2"}, "developer", "30m")
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

	_, err := ClaimTask(s, clock.FixedClock{T: t0}, lease.StaticID("x"), task.ID, lease.Auth{Agent: "agent2"}, "developer", "30m")
	if !errors.Is(err, ErrLegacyLease) {
		t.Errorf("expected ErrLegacyLease for claim over legacy lock, got: %v", err)
	}
}

func TestClaim_ExpiredReclaimReplacesOnlyTarget(t *testing.T) {
	s, _ := setupTestStore(t)
	taskA := addTestTask(t, s, "Task A", model.PriorityHigh, nil)
	taskB := addTestTask(t, s, "Task B", model.PriorityMedium, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)

	// A and B both hold expired NEW-format leases.
	expired := model.Lock{
		TaskID:        taskA.ID,
		Agent:         "old-a",
		LeaseID:       "lease-old-a",
		LeaseDuration: "30m",
		AcquiredAt:    t0.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:     t0.Add(-30 * time.Minute).Format(time.RFC3339),
		HeartbeatAt:   t0.Add(-time.Hour).Format(time.RFC3339),
	}
	expiredB := expired
	expiredB.TaskID = taskB.ID
	expiredB.Agent = "old-b"
	expiredB.LeaseID = "lease-old-b"
	writeRawLock(t, s, expired, expiredB)

	// Re-claim A: only A's entry is replaced with a NEW lease id; B is
	// preserved byte-for-byte.
	lock, err := ClaimTask(s, clk, newID, taskA.ID, lease.Auth{Agent: "new-a"}, "developer", "45m")
	if err != nil {
		t.Fatalf("re-claim failed: %v", err)
	}
	if lock.LeaseID == "lease-old-a" {
		t.Errorf("expected a new lease id, got the old one %q", lock.LeaseID)
	}

	locks := readLockList(t, s)
	if !hasLockFor(locks, taskA.ID) {
		t.Fatal("task A entry missing after re-claim")
	}
	foundB := false
	for _, l := range locks.Locks {
		if l.TaskID == taskB.ID {
			foundB = true
			if l.Agent != "old-b" || l.LeaseID != "lease-old-b" {
				t.Errorf("task B entry was modified: %+v", l)
			}
		}
	}
	if !foundB {
		t.Error("task B expired entry was silently removed")
	}
}

func TestClaim_DoesNotGloballyCleanExpiredLocks(t *testing.T) {
	s, _ := setupTestStore(t)
	taskA := addTestTask(t, s, "Task A", model.PriorityHigh, nil)
	taskB := addTestTask(t, s, "Task B", model.PriorityMedium, nil)
	taskC := addTestTask(t, s, "Task C", model.PriorityLow, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)

	expired := func(id, agent, leaseID string) model.Lock {
		return model.Lock{
			TaskID:        id,
			Agent:         agent,
			LeaseID:       leaseID,
			LeaseDuration: "30m",
			AcquiredAt:    t0.Add(-time.Hour).Format(time.RFC3339),
			ExpiresAt:     t0.Add(-30 * time.Minute).Format(time.RFC3339),
			HeartbeatAt:   t0.Add(-time.Hour).Format(time.RFC3339),
		}
	}
	writeRawLock(t, s, expired(taskA.ID, "a", "lease-a"), expired(taskB.ID, "b", "lease-b"))

	// Claiming an unrelated task C must NOT clean A/B's expired entries.
	if _, err := ClaimTask(s, clk, newID, taskC.ID, lease.Auth{Agent: "c"}, "developer", "30m"); err != nil {
		t.Fatalf("claim C failed: %v", err)
	}
	locks := readLockList(t, s)
	if !hasLockFor(locks, taskA.ID) || !hasLockFor(locks, taskB.ID) {
		t.Error("claim on unrelated task removed expired entries from other tasks")
	}
}

func TestClaim_ForceTakeoverCreatesNewLease(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Takeover", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk := clock.FixedClock{T: t0}
	// Sequential IDs so the takeover is guaranteed a DIFFERENT lease_id.
	seq := 0
	newID := lease.IDGen(func() string { seq++; return "lease-seq-" + strconv.Itoa(seq) })

	first := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

	lock, err := ClaimTask(s, clk, newID, task.ID, lease.Auth{Agent: "agent2", Force: true, Reason: "agent1 gone"}, "developer", "60m")
	if err != nil {
		t.Fatalf("force takeover failed: %v", err)
	}
	if lock.LeaseID == first.LeaseID {
		t.Errorf("expected a fresh lease id on takeover, got the same %q", lock.LeaseID)
	}
	events, _ := s.ReadEvents()
	if !hasEvent(events, model.EventTaskLeaseBroken) {
		t.Error("expected task.lease_broken event on forced takeover")
	}
}

func TestClaim_InvalidTTLFailsBeforeWrite(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Bad ttl", model.PriorityHigh, nil)

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)

	for _, ttl := range []string{"bogus", "0s", "-1m"} {
		_, err := ClaimTask(s, clk, newID, task.ID, lease.Auth{Agent: "agent1"}, "developer", ttl)
		if !errors.Is(err, ErrInvalidTTL) {
			t.Errorf("ttl %q: expected ErrInvalidTTL, got: %v", ttl, err)
		}
	}

	if len(readLockList(t, s).Locks) != 0 {
		t.Errorf("expected no lock written after invalid TTL")
	}
}

func TestCompleteTask_LeaseGate(t *testing.T) {
	t.Run("requires owner and lease-id", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Gate", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed})
		if !errors.Is(err, ErrLeaseRequired) {
			t.Errorf("expected ErrLeaseRequired without credentials, got: %v", err)
		}
		// Agent-only is also missing the lease-id.
		err = CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed, Agent: "agent1"})
		if !errors.Is(err, ErrLeaseRequired) {
			t.Errorf("expected ErrLeaseRequired for agent-only, got: %v", err)
		}
		updated, _ := GetTaskByID(s, task.ID)
		if updated.Status != model.StatusInProgress {
			t.Errorf("expected task untouched after blocked completion, got %s", updated.Status)
		}
	})

	t.Run("owner with exact lease-id completes and lease is released", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Owner done", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		if err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed, Agent: lock.Agent, LeaseID: lock.LeaseID}); err != nil {
			t.Fatalf("owner completion failed: %v", err)
		}
		if hasLockFor(readLockList(t, s), task.ID) {
			t.Error("expected lease released on done")
		}
	})

	t.Run("force without reason rejected", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Force gate", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed, Agent: "agent2", Force: true})
		if !errors.Is(err, ErrForceReasonRequired) {
			t.Errorf("expected ErrForceReasonRequired, got: %v", err)
		}
	})

	t.Run("force without actor rejected", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Force actor", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed, Force: true, Reason: "x"})
		if !errors.Is(err, ErrForceAgentRequired) {
			t.Errorf("expected ErrForceAgentRequired, got: %v", err)
		}
	})

	t.Run("force with reason and actor breaks lease", func(t *testing.T) {
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
		if !errors.Is(err, ErrLegacyLease) {
			t.Errorf("expected ErrLegacyLease, got: %v", err)
		}
		if err := CompleteTask(s, clock.FixedClock{T: t0}, task.ID, CompleteOptions{
			Result: model.TestResultPassed, Agent: "new-agent", Force: true, Reason: "migration",
		}); err != nil {
			t.Fatalf("legacy forced completion failed: %v", err)
		}
	})

	t.Run("expired lease fails closed", func(t *testing.T) {
		s, _ := setupTestStore(t)
		task := addTestTask(t, s, "Stale done", model.PriorityHigh, nil)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clk, newID := leaseFixture(t0)
		lock := mustLease(t, s, clk, newID, task.ID, "agent1", "30m")

		clk.T = t0.Add(31 * time.Minute)
		err := CompleteTask(s, clk, task.ID, CompleteOptions{Result: model.TestResultPassed, Agent: lock.Agent, LeaseID: lock.LeaseID})
		if !errors.Is(err, ErrLeaseExpired) {
			t.Fatalf("expected ErrLeaseExpired for completion over expired lease, got: %v", err)
		}
		if !hasLockFor(readLockList(t, s), task.ID) {
			t.Error("expired completion removed the entry; it must be preserved")
		}
	})
}

func TestNoRecordCompatibilityPath(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "No lease", model.PriorityHigh, nil)

	// start/done/block/note all succeed with empty auth when no record exists.
	if err := StartTask(s, clock.RealClock{}, task.ID, lease.Auth{}); err != nil {
		t.Errorf("no-record start should succeed, got: %v", err)
	}
	if err := NoteTask(s, clock.RealClock{}, task.ID, "note", lease.Auth{}); err != nil {
		t.Errorf("no-record note should succeed, got: %v", err)
	}
	if err := BlockTask(s, clock.RealClock{}, task.ID, "blocked", lease.Auth{}); err != nil {
		t.Errorf("no-record block should succeed, got: %v", err)
	}
	if err := CompleteTask(s, clock.RealClock{}, task.ID, CompleteOptions{Result: model.TestResultPassed}); err != nil {
		t.Errorf("no-record done should succeed, got: %v", err)
	}
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

// hasLockFor reports whether the lock list contains an entry for taskID.
func hasLockFor(locks *model.LockList, taskID string) bool {
	for _, l := range locks.Locks {
		if l.TaskID == taskID {
			return true
		}
	}
	return false
}

// hasEvent reports whether any event has the given type.
func hasEvent(events []model.Event, eventType string) bool {
	for _, e := range events {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

// auditEventFields holds the parsed fields of a task.lease_broken forced
// override audit message.
type auditEventFields struct {
	actor   string
	reason  string
	owner   string
	leaseID string
	state   string
	outcome string
}

// auditMsgRe matches the single stable forced-override audit format:
//
//	forced override by "<actor>" (reason: <reason>); previous owner="<owner>"
//	lease="<lease>" state=<state> (<outcome>)
var auditMsgRe = regexp.MustCompile(`^forced override by "([^"]*)" \(reason: (.*?)\); previous owner="([^"]*)" lease="([^"]*)" state=([a-z]+) \(([^)]*)\)$`)

// findBrokenAudit returns the parsed task.lease_broken audit event for taskID.
// Tests must assert on parsed fields, never on arbitrary substrings.
func findBrokenAudit(t *testing.T, s *store.Store, taskID string) auditEventFields {
	t.Helper()
	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	for _, e := range events {
		if e.Type == model.EventTaskLeaseBroken && e.TaskID == taskID {
			m := auditMsgRe.FindStringSubmatch(e.Message)
			if m == nil {
				t.Fatalf("lease_broken message does not match audit format: %q", e.Message)
			}
			return auditEventFields{actor: m[1], reason: m[2], owner: m[3], leaseID: m[4], state: m[5], outcome: m[6]}
		}
	}
	t.Fatalf("no task.lease_broken event for %s", taskID)
	return auditEventFields{}
}

// findClaimedMsg returns the message of the MOST RECENT task.claimed event
// for taskID (a task can be claimed more than once via re-claim/takeover).
func findClaimedMsg(t *testing.T, s *store.Store, taskID string) string {
	t.Helper()
	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == model.EventTaskClaimed && events[i].TaskID == taskID {
			return events[i].Message
		}
	}
	t.Fatalf("no task.claimed event for %s", taskID)
	return ""
}

// claimedReplacementRe matches the replaced-lease claim message:
//
//	replaced lease for task <id> (old owner "<owner>" lease "<lease>") with
//	lease <new> expires <expiry>
var claimedReplacementRe = regexp.MustCompile(`^replaced lease for task \S+ \(old owner "([^"]*)" lease "([^"]*)"\) with lease (\S+) expires \S+$`)

// parseClaimedReplacement extracts old owner, old lease, and new lease from a
// replacement claim message, failing the test on a format mismatch.
func parseClaimedReplacement(t *testing.T, msg string) (oldOwner, oldLease, newLease string) {
	t.Helper()
	m := claimedReplacementRe.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("claimed message does not match replacement format: %q", msg)
	}
	return m[1], m[2], m[3]
}

// TestSnapshotLock_SurvivesInPlaceReplacement directly proves the FA1 alias
// fix: a snapshot taken from a slice element must keep its pre-mutation
// values even after replaceTaskLock / removeTaskLock mutate the same slice.
// Reading the element pointer after replacement yields the NEW values, which
// is exactly the bug this snapshot exists to prevent.
func TestSnapshotLock_SurvivesInPlaceReplacement(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	locks := &model.LockList{Locks: []model.Lock{{
		TaskID:        "TASK-001",
		Agent:         "a1",
		Role:          "developer",
		LeaseID:       "lease-old",
		LeaseDuration: "30m",
		AcquiredAt:    t0.Format(time.RFC3339),
		ExpiresAt:     t0.Add(30 * time.Minute).Format(time.RFC3339),
		HeartbeatAt:   t0.Format(time.RFC3339),
	}}}
	existing := findTaskLock(locks, "TASK-001")
	prior := snapshotLock(existing, t0)

	// In-place replacement of the same slice element must not corrupt it.
	replaceTaskLock(locks, "TASK-001", model.Lock{TaskID: "TASK-001", Agent: "admin", LeaseID: "lease-new"})
	if prior.owner != "a1" || prior.leaseID != "lease-old" || prior.state != "active" {
		t.Errorf("snapshot corrupted by in-place replacement: %+v", prior)
	}

	// Removal must not corrupt it either.
	removeTaskLock(locks, "TASK-001")
	if prior.owner != "a1" || prior.leaseID != "lease-old" || prior.state != "active" {
		t.Errorf("snapshot corrupted by removal: %+v", prior)
	}
}

// TestForceClaimAudit_ActiveUsesPriorSnapshot verifies a forced takeover of an
// ACTIVE lease audits the real prior owner/lease/state (FA1): the old fields
// are never aliased to the new owner or the new lease-id.
func TestForceClaimAudit_ActiveUsesPriorSnapshot(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Takeover audit", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk := clock.FixedClock{T: t0}
	seq := 0
	newID := lease.IDGen(func() string { seq++; return "lease-seq-" + strconv.Itoa(seq) })

	first := mustLease(t, s, clk, newID, task.ID, "a1", "30m")
	lock, err := ClaimTask(s, clk, newID, task.ID, lease.Auth{Agent: "admin", Force: true, Reason: "takeover"}, "developer", "60m")
	if err != nil {
		t.Fatalf("force takeover failed: %v", err)
	}
	if lock.LeaseID == first.LeaseID {
		t.Fatalf("expected a fresh lease id on takeover, got %q", lock.LeaseID)
	}

	// The claimed event must carry the REAL prior owner/lease, not the new ones.
	oldOwner, oldLease, newLease := parseClaimedReplacement(t, findClaimedMsg(t, s, task.ID))
	if oldOwner != "a1" || oldLease != first.LeaseID {
		t.Errorf("claimed event old fields wrong: owner=%q lease=%q", oldOwner, oldLease)
	}
	if newLease != lock.LeaseID || newLease == oldLease {
		t.Errorf("claimed event new lease wrong: new=%q old=%q", newLease, oldLease)
	}

	audit := findBrokenAudit(t, s, task.ID)
	if audit.actor != "admin" || audit.reason != "takeover" {
		t.Errorf("audit actor/reason wrong: %+v", audit)
	}
	if audit.owner != "a1" || audit.leaseID != first.LeaseID || audit.state != "active" {
		t.Errorf("audit prior fields wrong: %+v", audit)
	}
	if audit.outcome != "lease replaced" {
		t.Errorf("unexpected outcome: %q", audit.outcome)
	}
	if audit.owner == "admin" || audit.leaseID == lock.LeaseID {
		t.Errorf("audit old fields polluted by new values: %+v", audit)
	}
}

// TestForceClaimAudit_Expired verifies a forced takeover of an EXPIRED lease
// reports state=expired with the prior owner/lease.
func TestForceClaimAudit_Expired(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Expired takeover", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeRawLock(t, s, model.Lock{
		TaskID:        task.ID,
		Agent:         "a1",
		Role:          "developer",
		LeaseID:       "lease-old",
		LeaseDuration: "30m",
		AcquiredAt:    t0.Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt:     t0.Add(-time.Hour).Format(time.RFC3339),
		HeartbeatAt:   t0.Add(-2 * time.Hour).Format(time.RFC3339),
	})
	clk := clock.FixedClock{T: t0}
	lock, err := ClaimTask(s, clk, lease.StaticID("lease-new"), task.ID, lease.Auth{Agent: "admin", Force: true, Reason: "recover"}, "developer", "60m")
	if err != nil {
		t.Fatalf("forced claim over expired lease failed: %v", err)
	}
	if lock.LeaseID != "lease-new" {
		t.Fatalf("expected lease-new, got %q", lock.LeaseID)
	}
	audit := findBrokenAudit(t, s, task.ID)
	if audit.owner != "a1" || audit.leaseID != "lease-old" || audit.state != "expired" {
		t.Errorf("audit prior fields wrong: %+v", audit)
	}
	if audit.outcome != "lease replaced" {
		t.Errorf("unexpected outcome: %q", audit.outcome)
	}
}

// TestForceClaimAudit_Legacy verifies a forced takeover of a LEGACY record
// keeps the old owner, marks the old lease-id as empty, and reports
// state=legacy (never claiming the new lease-id as the old one).
func TestForceClaimAudit_Legacy(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Legacy takeover", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeRawLock(t, s, model.Lock{
		TaskID:     task.ID,
		Agent:      "old-agent",
		Role:       "developer",
		AcquiredAt: t0.Format(time.RFC3339),
		ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
	})
	clk := clock.FixedClock{T: t0}
	lock, err := ClaimTask(s, clk, lease.StaticID("lease-new"), task.ID, lease.Auth{Agent: "admin", Force: true, Reason: "migrate"}, "developer", "60m")
	if err != nil {
		t.Fatalf("forced claim over legacy lock failed: %v", err)
	}
	if lock.LeaseID != "lease-new" {
		t.Fatalf("expected lease-new, got %q", lock.LeaseID)
	}
	audit := findBrokenAudit(t, s, task.ID)
	if audit.owner != "old-agent" || audit.leaseID != "" || audit.state != "legacy" {
		t.Errorf("audit prior fields wrong: %+v", audit)
	}
	if audit.leaseID == "lease-new" {
		t.Errorf("legacy audit must not claim the new lease as the old lease: %+v", audit)
	}
	oldOwner, oldLease, newLease := parseClaimedReplacement(t, findClaimedMsg(t, s, task.ID))
	if oldOwner != "old-agent" || oldLease != "" || newLease != "lease-new" {
		t.Errorf("claimed event fields wrong: owner=%q lease=%q new=%q", oldOwner, oldLease, newLease)
	}
}

// TestOrdinaryExpiredReclaim_DoesNotAliasPrior verifies that an ordinary
// same-task re-claim of an expired lease reports the true old owner/lease in
// the task.claimed message (the pointer-alias bug also affected this path)
// and does not write a lease_broken override event.
func TestOrdinaryExpiredReclaim_DoesNotAliasPrior(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "Expired reclaim", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk := clock.FixedClock{T: t0}
	seq := 0
	newID := lease.IDGen(func() string { seq++; return "lease-seq-" + strconv.Itoa(seq) })
	first := mustLease(t, s, clk, newID, task.ID, "a1", "30m")

	clk.T = t0.Add(31 * time.Minute) // lease is expired now
	lock, err := ClaimTask(s, clk, newID, task.ID, lease.Auth{Agent: "b1"}, "developer", "30m")
	if err != nil {
		t.Fatalf("expired re-claim failed: %v", err)
	}
	oldOwner, oldLease, newLease := parseClaimedReplacement(t, findClaimedMsg(t, s, task.ID))
	if oldOwner != "a1" || oldLease != first.LeaseID {
		t.Errorf("claimed event old fields aliased: owner=%q lease=%q", oldOwner, oldLease)
	}
	if newLease != lock.LeaseID || newLease == oldLease {
		t.Errorf("claimed event new lease wrong: new=%q old=%q", newLease, oldLease)
	}
	events, _ := s.ReadEvents()
	if hasEvent(events, model.EventTaskLeaseBroken) {
		t.Error("ordinary re-claim must not write a lease_broken override event")
	}
}

// TestForceReleaseAudit_Fields verifies forced release audits the full prior
// owner/lease/state for active, expired, and legacy records (FA2) and removes
// only the target lease.
func TestForceReleaseAudit_Fields(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	activeLock := func(s *store.Store, taskID string) {
		mustLease(t, s, clock.FixedClock{T: t0}, lease.StaticID("lease-old"), taskID, "a1", "30m")
	}
	expiredLock := func(s *store.Store, taskID string) {
		writeRawLock(t, s, model.Lock{
			TaskID: taskID, Agent: "a1", Role: "developer",
			LeaseID: "lease-old", LeaseDuration: "30m",
			AcquiredAt:  t0.Add(-2 * time.Hour).Format(time.RFC3339),
			ExpiresAt:   t0.Add(-time.Hour).Format(time.RFC3339),
			HeartbeatAt: t0.Add(-2 * time.Hour).Format(time.RFC3339),
		})
	}
	legacyLock := func(s *store.Store, taskID string) {
		writeRawLock(t, s, model.Lock{
			TaskID: taskID, Agent: "old-agent", Role: "developer",
			AcquiredAt: t0.Format(time.RFC3339),
			ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
		})
	}

	cases := []struct {
		name  string
		plant func(s *store.Store, taskID string)
		owner string
		lease string
		state string
	}{
		{"active", activeLock, "a1", "lease-old", "active"},
		{"expired", expiredLock, "a1", "lease-old", "expired"},
		{"legacy", legacyLock, "old-agent", "", "legacy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setupTestStore(t)
			task := addTestTask(t, s, "Force release "+tc.name, model.PriorityHigh, nil)
			tc.plant(s, task.ID)

			if err := ReleaseTask(s, clock.FixedClock{T: t0}, task.ID, lease.Auth{Agent: "admin", Force: true, Reason: "break"}); err != nil {
				t.Fatalf("forced release failed: %v", err)
			}
			if hasLockFor(readLockList(t, s), task.ID) {
				t.Error("forced release did not remove the target lease")
			}
			audit := findBrokenAudit(t, s, task.ID)
			if audit.actor != "admin" || audit.reason != "break" ||
				audit.owner != tc.owner || audit.leaseID != tc.lease || audit.state != tc.state ||
				audit.outcome != "lease removed" {
				t.Errorf("audit fields wrong: %+v", audit)
			}
		})
	}
}

// TestForceDoneAudit_Fields verifies forced completion audits the full prior
// owner/lease/state for active, expired, and legacy records (FA2), completes
// the task, and removes only the target lease.
func TestForceDoneAudit_Fields(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	activeLock := func(s *store.Store, taskID string) {
		mustLease(t, s, clock.FixedClock{T: t0}, lease.StaticID("lease-old"), taskID, "a1", "30m")
	}
	expiredLock := func(s *store.Store, taskID string) {
		writeRawLock(t, s, model.Lock{
			TaskID: taskID, Agent: "a1", Role: "developer",
			LeaseID: "lease-old", LeaseDuration: "30m",
			AcquiredAt:  t0.Add(-2 * time.Hour).Format(time.RFC3339),
			ExpiresAt:   t0.Add(-time.Hour).Format(time.RFC3339),
			HeartbeatAt: t0.Add(-2 * time.Hour).Format(time.RFC3339),
		})
	}
	legacyLock := func(s *store.Store, taskID string) {
		writeRawLock(t, s, model.Lock{
			TaskID: taskID, Agent: "old-agent", Role: "developer",
			AcquiredAt: t0.Format(time.RFC3339),
			ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
		})
	}

	cases := []struct {
		name  string
		plant func(s *store.Store, taskID string)
		owner string
		lease string
		state string
	}{
		{"active", activeLock, "a1", "lease-old", "active"},
		{"expired", expiredLock, "a1", "lease-old", "expired"},
		{"legacy", legacyLock, "old-agent", "", "legacy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setupTestStore(t)
			task := addTestTask(t, s, "Force done "+tc.name, model.PriorityHigh, nil)
			tc.plant(s, task.ID)

			if err := CompleteTask(s, clock.FixedClock{T: t0}, task.ID, CompleteOptions{
				Result: model.TestResultPassed, Agent: "admin", Force: true, Reason: "break",
			}); err != nil {
				t.Fatalf("forced completion failed: %v", err)
			}
			updated, _ := GetTaskByID(s, task.ID)
			if updated.Status != model.StatusDone {
				t.Errorf("expected task done, got %s", updated.Status)
			}
			if hasLockFor(readLockList(t, s), task.ID) {
				t.Error("forced completion did not remove the target lease")
			}
			audit := findBrokenAudit(t, s, task.ID)
			if audit.actor != "admin" || audit.reason != "break" ||
				audit.owner != tc.owner || audit.leaseID != tc.lease || audit.state != tc.state ||
				audit.outcome != "lease removed" {
				t.Errorf("audit fields wrong: %+v", audit)
			}
		})
	}
}

// TestForceAudit_StartBlockNoteRetainedFormat verifies start/block/note still
// emit the same full prior-field audit format (outcome "lease retained") and
// never remove the lease, so the shared helper did not regress them.
func TestForceAudit_StartBlockNoteRetainedFormat(t *testing.T) {
	// start/block/note classify the prior state with the real clock
	// (recordForcedOverride's frozen behavior), so plant the lease relative
	// to real now with a wide margin to make state deterministically active.
	t0 := time.Now().UTC()
	runs := []struct {
		name string
		run  func(t *testing.T, s *store.Store, taskID string)
	}{
		{"start", func(t *testing.T, s *store.Store, taskID string) {
			if err := StartTask(s, clock.FixedClock{T: t0}, taskID, lease.Auth{Agent: "admin", Force: true, Reason: "override"}); err != nil {
				t.Fatalf("forced start failed: %v", err)
			}
		}},
		{"block", func(t *testing.T, s *store.Store, taskID string) {
			if err := BlockTask(s, clock.FixedClock{T: t0}, taskID, "blocked", lease.Auth{Agent: "admin", Force: true, Reason: "override"}); err != nil {
				t.Fatalf("forced block failed: %v", err)
			}
		}},
		{"note", func(t *testing.T, s *store.Store, taskID string) {
			if err := NoteTask(s, clock.FixedClock{T: t0}, taskID, "n", lease.Auth{Agent: "admin", Force: true, Reason: "override"}); err != nil {
				t.Fatalf("forced note failed: %v", err)
			}
		}},
	}
	for _, tc := range runs {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := setupTestStore(t)
			task := addTestTask(t, s, "Shared format "+tc.name, model.PriorityHigh, nil)
			mustLease(t, s, clock.FixedClock{T: t0}, lease.StaticID("lease-old"), task.ID, "a1", "30m")

			tc.run(t, s, task.ID)

			audit := findBrokenAudit(t, s, task.ID)
			if audit.actor != "admin" || audit.reason != "override" ||
				audit.owner != "a1" || audit.leaseID != "lease-old" || audit.state != "active" ||
				audit.outcome != "lease retained" {
				t.Errorf("audit fields wrong: %+v", audit)
			}
			if !hasLockFor(readLockList(t, s), task.ID) {
				t.Error("start/block/note must retain the lease")
			}
		})
	}
}

// TestForceValidationFailure_NoOverrideEvent verifies that an invalid --force
// (missing reason or actor) never writes a lease_broken override event and
// never removes the lease.
func TestForceValidationFailure_NoOverrideEvent(t *testing.T) {
	s, _ := setupTestStore(t)
	task := addTestTask(t, s, "No audit on invalid force", model.PriorityHigh, nil)
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clk, newID := leaseFixture(t0)
	mustLease(t, s, clk, newID, task.ID, "a1", "30m")

	for _, auth := range []lease.Auth{
		{Agent: "admin", Force: true}, // missing reason
		{Force: true, Reason: "x"},    // missing actor
		{Force: true},                 // both missing
	} {
		err := ReleaseTask(s, clk, task.ID, auth)
		if !errors.Is(err, ErrForceReasonRequired) && !errors.Is(err, ErrForceAgentRequired) {
			t.Fatalf("expected force validation error, got: %v", err)
		}
	}
	events, _ := s.ReadEvents()
	if hasEvent(events, model.EventTaskLeaseBroken) {
		t.Error("invalid force must not write a lease_broken audit event")
	}
	if !hasLockFor(readLockList(t, s), task.ID) {
		t.Error("invalid force removed the lease")
	}
}
