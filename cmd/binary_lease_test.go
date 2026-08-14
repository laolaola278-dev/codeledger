package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// initDir initializes a fresh .ctask project in a temp dir via the real
// binary and returns the dir.
func initBinDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return dir
}

// addBinTask adds a task via the real binary and returns its ID.
func addBinTask(t *testing.T, dir string) string {
	t.Helper()
	if r := runBin(t, dir, "add", "Binary task"); r.code != 0 {
		t.Fatalf("add failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return "TASK-001"
}

// TestBinary_ClaimHeartbeatReleaseFlow exercises the full lease lifecycle
// through the real process boundary.
func TestBinary_ClaimHeartbeatReleaseFlow(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)

	r := runBin(t, dir, "claim", "TASK-001", "--agent", "codex", "--ttl", "30m")
	if r.code != 0 {
		t.Fatalf("claim failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "Lease ID: lease-") {
		t.Errorf("expected a lease id in claim output, got: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "expires") {
		t.Errorf("expected expiry in claim output, got: %q", r.stdout)
	}
	if r.stderr != "" {
		t.Errorf("expected empty stderr on claim success, got: %q", r.stderr)
	}

	// The lease must be recorded in locks.yaml with the lease fields.
	s := store.NewStore(dir)
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	found := false
	for _, l := range locks.Locks {
		if l.TaskID == "TASK-001" && l.Agent == "codex" {
			found = true
			if l.LeaseID == "" || l.LeaseDuration == "" {
				t.Errorf("lease fields missing: %+v", l)
			}
		}
	}
	if !found {
		t.Fatal("expected lease for TASK-001 in locks.yaml")
	}

	// Heartbeat by the owner renews.
	r = runBin(t, dir, "heartbeat", "TASK-001", "--agent", "codex")
	if r.code != 0 {
		t.Fatalf("heartbeat failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "renewed") {
		t.Errorf("expected renewal output, got: %q", r.stdout)
	}

	// Release by the owner.
	r = runBin(t, dir, "release", "TASK-001", "--agent", "codex")
	if r.code != 0 {
		t.Fatalf("release failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	locks, _ = s.ReadLocks()
	for _, l := range locks.Locks {
		if l.TaskID == "TASK-001" {
			t.Error("expected lease removed after release")
		}
	}
}

// TestBinary_ClaimConflict_Exit3 verifies a second agent cannot claim a task
// with an active lease (LEASE_CONFLICT -> exit 3).
func TestBinary_ClaimConflict_Exit3(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)

	if r := runBin(t, dir, "claim", "TASK-001", "--agent", "agent1", "--ttl", "30m"); r.code != 0 {
		t.Fatalf("first claim failed: %d", r.code)
	}
	r := runBin(t, dir, "claim", "TASK-001", "--agent", "agent2", "--ttl", "30m")
	if r.code != 3 {
		t.Errorf("expected exit 3, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "lease conflict") {
		t.Errorf("expected lease conflict on stderr, got: %q", r.stderr)
	}
}

// TestBinary_ReleaseForceWithoutReason_Exit2 verifies --force requires an
// explicit --reason (FORCE_REQUIRED -> exit 2).
func TestBinary_ReleaseForceWithoutReason_Exit2(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)

	if r := runBin(t, dir, "claim", "TASK-001", "--agent", "agent1", "--ttl", "30m"); r.code != 0 {
		t.Fatalf("claim failed: %d", r.code)
	}
	r := runBin(t, dir, "release", "TASK-001", "--agent", "agent2", "--force")
	if r.code != 2 {
		t.Errorf("expected exit 2, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "--force requires --reason") {
		t.Errorf("expected force-reason message on stderr, got: %q", r.stderr)
	}
	// With a reason the same command succeeds.
	r = runBin(t, dir, "release", "TASK-001", "--agent", "agent2", "--force", "--reason", "cleanup")
	if r.code != 0 {
		t.Errorf("expected exit 0 for forced release with reason, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
}

// TestBinary_DoneLeaseGate verifies completion of a leased task requires the
// owner, and that the owner can complete it.
func TestBinary_DoneLeaseGate(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)

	if r := runBin(t, dir, "claim", "TASK-001", "--agent", "agent1", "--ttl", "30m"); r.code != 0 {
		t.Fatalf("claim failed: %d", r.code)
	}
	// No owner given: blocked with exit 3.
	r := runBin(t, dir, "done", "TASK-001", "--result", "passed")
	if r.code != 3 {
		t.Errorf("expected exit 3 for done without owner, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	// Owner completes successfully.
	r = runBin(t, dir, "done", "TASK-001", "--result", "passed", "--agent", "agent1")
	if r.code != 0 {
		t.Errorf("expected exit 0 for owner done, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	// The lease was auto-released on completion.
	s := store.NewStore(dir)
	locks, err := s.ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	for _, l := range locks.Locks {
		if l.TaskID == "TASK-001" {
			t.Error("expected lease auto-released on done")
		}
	}
}

// TestBinary_LegacyLockFailClosed verifies pre-P1 locks are handled
// fail-closed: they block claims and plain releases, and require an explicit
// --force --reason to clear.
func TestBinary_LegacyLockFailClosed(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)

	// Plant a legacy lock (no lease_id / lease_duration) with a future expiry
	// relative to the real clock so it counts as active/fail-closed.
	s := store.NewStore(dir)
	now := time.Now().UTC()
	if err := s.WriteLocks(&model.LockList{Locks: []model.Lock{{
		TaskID:     "TASK-001",
		Agent:      "old-agent",
		Role:       "developer",
		AcquiredAt: now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt:  now.Add(2 * time.Hour).Format(time.RFC3339),
	}}}); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}

	// Claim is blocked fail-closed (LEGACY_STATE -> exit 1).
	r := runBin(t, dir, "claim", "TASK-001", "--agent", "new-agent", "--ttl", "30m")
	if r.code != 1 {
		t.Errorf("expected exit 1 for claim over legacy lock, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "legacy") {
		t.Errorf("expected legacy message on stderr, got: %q", r.stderr)
	}

	// Plain release is blocked too.
	r = runBin(t, dir, "release", "TASK-001", "--agent", "old-agent")
	if r.code != 1 {
		t.Errorf("expected exit 1 for legacy release without force, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}

	// --force --reason clears it.
	r = runBin(t, dir, "release", "TASK-001", "--agent", "old-agent", "--force", "--reason", "migrating legacy lock")
	if r.code != 0 {
		t.Errorf("expected exit 0 for legacy force release, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}

	// The task is claimable again.
	r = runBin(t, dir, "claim", "TASK-001", "--agent", "new-agent", "--ttl", "30m")
	if r.code != 0 {
		t.Errorf("expected claim to succeed after legacy cleanup, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
}
