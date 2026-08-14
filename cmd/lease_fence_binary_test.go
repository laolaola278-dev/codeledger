package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// writeExpiredLease plants a well-formed (non-legacy) EXPIRED lease for
// taskID into locks.yaml so ordinary operations must fail closed with
// LEASE_EXPIRED and the entry must be preserved.
func writeExpiredLease(t *testing.T, dir, taskID, agent, leaseID string) {
	t.Helper()
	now := time.Now().UTC()
	locks := &model.LockList{Locks: []model.Lock{{
		TaskID:        taskID,
		Agent:         agent,
		Role:          "developer",
		LeaseID:       leaseID,
		LeaseDuration: "30m",
		AcquiredAt:    now.Add(-2 * time.Hour).Format(time.RFC3339),
		ExpiresAt:     now.Add(-1 * time.Hour).Format(time.RFC3339),
		HeartbeatAt:   now.Add(-2 * time.Hour).Format(time.RFC3339),
	}}}
	if err := store.NewStore(dir).WriteLocks(locks); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}
}

// binHasLock reports whether locks.yaml still contains an entry for taskID.
func binHasLock(t *testing.T, dir, taskID string) bool {
	t.Helper()
	locks, err := store.NewStore(dir).ReadLocks()
	if err != nil {
		t.Fatalf("ReadLocks failed: %v", err)
	}
	for _, l := range locks.Locks {
		if l.TaskID == taskID {
			return true
		}
	}
	return false
}

// TestBinary_ClaimHeartbeatJSON verifies claim --json and heartbeat --json
// each emit exactly one parseable JSON document with the required fields.
func TestBinary_ClaimHeartbeatJSON(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)

	r := runBin(t, dir, "claim", "TASK-001", "--agent", "codex", "--ttl", "30m", "--json")
	if r.code != 0 {
		t.Fatalf("claim --json failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	var claim struct {
		TaskID               string `json:"task_id"`
		Agent                string `json:"agent"`
		LeaseID              string `json:"lease_id"`
		ExpiresAt            string `json:"expires_at"`
		LeaseDurationSeconds int64  `json:"lease_duration_seconds"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &claim); err != nil {
		t.Fatalf("claim --json stdout is not a single JSON document: %v\n%s", err, r.stdout)
	}
	if claim.TaskID != "TASK-001" || claim.Agent != "codex" || claim.LeaseID == "" ||
		claim.ExpiresAt == "" || claim.LeaseDurationSeconds != 1800 {
		t.Errorf("unexpected claim JSON: %+v", claim)
	}

	leaseID := claim.LeaseID
	r = runBin(t, dir, "heartbeat", "TASK-001", "--agent", "codex", "--lease-id", leaseID, "--json")
	if r.code != 0 {
		t.Fatalf("heartbeat --json failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	var hb struct {
		TaskID    string `json:"task_id"`
		Agent     string `json:"agent"`
		LeaseID   string `json:"lease_id"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &hb); err != nil {
		t.Fatalf("heartbeat --json stdout is not a single JSON document: %v\n%s", err, r.stdout)
	}
	if hb.TaskID != "TASK-001" || hb.Agent != "codex" || hb.LeaseID != leaseID || hb.ExpiresAt == "" {
		t.Errorf("unexpected heartbeat JSON: %+v", hb)
	}
}

// TestBinary_LeaseFailureJSONEnvelopes verifies each stable lease/auth
// failure renders exactly one JSON error document with the right machine code.
func TestBinary_LeaseFailureJSONEnvelopes(t *testing.T) {
	t.Run("LEASE_REQUIRED", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m")
		r := runBin(t, dir, "heartbeat", "TASK-001", "--agent", "a1", "--json")
		assertJSONErrorEnvelope(t, r, 3, "LEASE_REQUIRED")
	})

	t.Run("LEASE_MISMATCH", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m")
		leaseID := readBinLeaseID(t, dir, "TASK-001")
		r := runBin(t, dir, "heartbeat", "TASK-001", "--agent", "a2", "--lease-id", leaseID, "--json")
		assertJSONErrorEnvelope(t, r, 3, "LEASE_MISMATCH")
	})

	t.Run("LEASE_EXPIRED", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		writeExpiredLease(t, dir, "TASK-001", "a1", "lease-old")
		r := runBin(t, dir, "heartbeat", "TASK-001", "--agent", "a1", "--lease-id", "lease-old", "--json")
		assertJSONErrorEnvelope(t, r, 3, "LEASE_EXPIRED")
	})

	t.Run("LEGACY_LEASE_REQUIRES_TAKEOVER", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		now := time.Now().UTC()
		locks := &model.LockList{Locks: []model.Lock{{
			TaskID:     "TASK-001",
			Agent:      "old-agent",
			Role:       "developer",
			AcquiredAt: now.Format(time.RFC3339),
			ExpiresAt:  now.Add(2 * time.Hour).Format(time.RFC3339),
		}}}
		if err := store.NewStore(dir).WriteLocks(locks); err != nil {
			t.Fatalf("WriteLocks failed: %v", err)
		}
		r := runBin(t, dir, "claim", "TASK-001", "--agent", "new-agent", "--ttl", "30m", "--json")
		assertJSONErrorEnvelope(t, r, 3, "LEGACY_LEASE_REQUIRES_TAKEOVER")
	})

	t.Run("FORCE_REASON_REQUIRED", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m")
		r := runBin(t, dir, "claim", "TASK-001", "--agent", "a2", "--force", "--json")
		assertJSONErrorEnvelope(t, r, 2, "FORCE_REASON_REQUIRED")
	})
}

// assertJSONErrorEnvelope asserts the subprocess failed with the given exit
// code and emitted exactly one JSON error document with the given code.
func assertJSONErrorEnvelope(t *testing.T, r binResult, exit int, code string) {
	t.Helper()
	if r.code != exit {
		t.Fatalf("expected exit %d, got %d\nstdout:\n%s\nstderr:\n%s", exit, r.code, r.stdout, r.stderr)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &envelope); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%s", err, r.stdout)
	}
	if envelope.OK {
		t.Error("expected ok=false")
	}
	if envelope.Error.Code != code {
		t.Errorf("expected machine code %q, got %q", code, envelope.Error.Code)
	}
	if r.stderr != "" {
		t.Errorf("expected empty stderr on JSON failure, got: %q", r.stderr)
	}
}

// TestBinary_ActiveFenceStartBlockNote verifies start/block/note reject
// missing credentials on an active lease and accept the exact owner.
func TestBinary_ActiveFenceStartBlockNote(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)
	if r := runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m"); r.code != 0 {
		t.Fatalf("claim failed: %d", r.code)
	}
	leaseID := readBinLeaseID(t, dir, "TASK-001")

	for _, args := range [][]string{
		{"start", "TASK-001"},
		{"start", "TASK-001", "--agent", "a1"},
		{"block", "TASK-001", "r"},
		{"note", "TASK-001", "n"},
	} {
		r := runBin(t, dir, args...)
		if r.code != 3 {
			t.Errorf("expected exit 3 for %v, got %d\nstderr: %s", args, r.code, r.stderr)
		}
	}

	// Exact owner succeeds.
	if r := runBin(t, dir, "start", "TASK-001", "--agent", "a1", "--lease-id", leaseID); r.code != 0 {
		t.Errorf("exact start failed: exit %d\nstderr: %s", r.code, r.stderr)
	}
	if r := runBin(t, dir, "note", "TASK-001", "n", "--agent", "a1", "--lease-id", leaseID); r.code != 0 {
		t.Errorf("exact note failed: exit %d\nstderr: %s", r.code, r.stderr)
	}
}

// TestBinary_ExpiredOrdinaryReleaseFailClosed verifies ordinary release of an
// expired lease exits 3 and preserves the entry.
func TestBinary_ExpiredOrdinaryReleaseFailClosed(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)
	writeExpiredLease(t, dir, "TASK-001", "a1", "lease-old")

	r := runBin(t, dir, "release", "TASK-001", "--agent", "a1", "--lease-id", "lease-old")
	if r.code != 3 {
		t.Fatalf("expected exit 3 for expired release, got %d\nstderr: %s", r.code, r.stderr)
	}
	if !binHasLock(t, dir, "TASK-001") {
		t.Error("expired release removed the entry; it must be preserved")
	}
}

// TestBinary_UnrelatedClaimPreservesExpired verifies claiming another task
// does not sweep unrelated expired entries.
func TestBinary_UnrelatedClaimPreservesExpired(t *testing.T) {
	dir := initBinDir(t)
	runBin(t, dir, "add", "Task A")
	runBin(t, dir, "add", "Task B")

	now := time.Now().UTC()
	expired := func(taskID, agent, leaseID string) model.Lock {
		return model.Lock{
			TaskID:        taskID,
			Agent:         agent,
			Role:          "developer",
			LeaseID:       leaseID,
			LeaseDuration: "30m",
			AcquiredAt:    now.Add(-2 * time.Hour).Format(time.RFC3339),
			ExpiresAt:     now.Add(-1 * time.Hour).Format(time.RFC3339),
			HeartbeatAt:   now.Add(-2 * time.Hour).Format(time.RFC3339),
		}
	}
	if err := store.NewStore(dir).WriteLocks(&model.LockList{Locks: []model.Lock{
		expired("TASK-001", "a1", "lease-a"),
		expired("TASK-002", "a2", "lease-b"),
	}}); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}

	if r := runBin(t, dir, "claim", "TASK-002", "--agent", "b2", "--ttl", "30m"); r.code != 0 {
		t.Fatalf("re-claim TASK-002 failed: %d\nstderr: %s", r.code, r.stderr)
	}
	// TASK-001's expired entry must be preserved; TASK-002's was replaced.
	if !binHasLock(t, dir, "TASK-001") {
		t.Error("claiming TASK-002 removed TASK-001's expired entry")
	}
	if !binHasLock(t, dir, "TASK-002") {
		t.Error("TASK-002 entry missing after re-claim")
	}
}

// TestBinary_ForceValidationExit2 verifies force without reason/actor exits 2.
func TestBinary_ForceValidationExit2(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)
	if r := runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m"); r.code != 0 {
		t.Fatalf("claim failed: %d", r.code)
	}

	// Missing reason.
	if r := runBin(t, dir, "release", "TASK-001", "--agent", "admin", "--force"); r.code != 2 {
		t.Errorf("expected exit 2 for force without reason, got %d\nstderr: %s", r.code, r.stderr)
	}
	// Missing actor.
	if r := runBin(t, dir, "release", "TASK-001", "--force", "--reason", "x"); r.code != 2 {
		t.Errorf("expected exit 2 for force without actor, got %d\nstderr: %s", r.code, r.stderr)
	}
	if !binHasLock(t, dir, "TASK-001") {
		t.Error("invalid force removed the lease")
	}
}

// TestBinary_NoRecordCompatibility verifies start/done/block/note still work
// without any lease flags when no record exists.
func TestBinary_NoRecordCompatibility(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)

	if r := runBin(t, dir, "start", "TASK-001"); r.code != 0 {
		t.Errorf("no-record start failed: exit %d\nstderr: %s", r.code, r.stderr)
	}
	if r := runBin(t, dir, "note", "TASK-001", "hello"); r.code != 0 {
		t.Errorf("no-record note failed: exit %d\nstderr: %s", r.code, r.stderr)
	}
	if r := runBin(t, dir, "block", "TASK-001", "blocked"); r.code != 0 {
		t.Errorf("no-record block failed: exit %d\nstderr: %s", r.code, r.stderr)
	}
	if r := runBin(t, dir, "done", "TASK-001", "--result", "passed"); r.code != 0 {
		t.Errorf("no-record done failed: exit %d\nstderr: %s", r.code, r.stderr)
	}
}

// TestBinary_JSONFalseUsesText verifies --json=false renders text errors on
// stderr (not a JSON document on stdout).
func TestBinary_JSONFalseUsesText(t *testing.T) {
	dir := initBinDir(t)
	addBinTask(t, dir)
	if r := runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m"); r.code != 0 {
		t.Fatalf("claim failed: %d", r.code)
	}
	r := runBin(t, dir, "heartbeat", "TASK-001", "--agent", "a1", "--json=false")
	if r.code != 3 {
		t.Errorf("expected exit 3, got %d", r.code)
	}
	if strings.TrimSpace(r.stdout) != "" {
		t.Errorf("expected empty stdout in text mode, got: %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "lease") {
		t.Errorf("expected text error on stderr, got: %q", r.stderr)
	}
}

// writeBinLegacyLock plants a pre-P1 legacy lock (no lease_id) for taskID in
// a binary-test dir.
func writeBinLegacyLock(t *testing.T, dir, taskID, agent string) {
	t.Helper()
	now := time.Now().UTC()
	locks := &model.LockList{Locks: []model.Lock{{
		TaskID:     taskID,
		Agent:      agent,
		Role:       "developer",
		AcquiredAt: now.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:  now.Add(2 * time.Hour).Format(time.RFC3339),
	}}}
	if err := store.NewStore(dir).WriteLocks(locks); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}
}

// binAudit holds the parsed fields of a task.lease_broken forced-override
// audit event.
type binAudit struct {
	actor, reason, owner, leaseID, state, outcome string
}

// binAuditRe matches the single stable forced-override audit format.
var binAuditRe = regexp.MustCompile(`^forced override by "([^"]*)" \(reason: (.*?)\); previous owner="([^"]*)" lease="([^"]*)" state=([a-z]+) \(([^)]*)\)$`)

// readBinAudit parses the task.lease_broken audit event for taskID from the
// real binary's events, failing if absent or malformed.
func readBinAudit(t *testing.T, dir, taskID string) binAudit {
	t.Helper()
	events, err := store.NewStore(dir).ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	for _, e := range events {
		if e.Type == model.EventTaskLeaseBroken && e.TaskID == taskID {
			m := binAuditRe.FindStringSubmatch(e.Message)
			if m == nil {
				t.Fatalf("lease_broken message does not match audit format: %q", e.Message)
			}
			return binAudit{actor: m[1], reason: m[2], owner: m[3], leaseID: m[4], state: m[5], outcome: m[6]}
		}
	}
	t.Fatalf("no task.lease_broken event for %s", taskID)
	return binAudit{}
}

// readBinClaimedMsg returns the most recent task.claimed message for taskID.
func readBinClaimedMsg(t *testing.T, dir, taskID string) string {
	t.Helper()
	events, err := store.NewStore(dir).ReadEvents()
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

// binClaimedRe matches the replaced-lease claim message.
var binClaimedRe = regexp.MustCompile(`^replaced lease for task \S+ \(old owner "([^"]*)" lease "([^"]*)"\) with lease (\S+) expires \S+$`)

// parseBinClaimedReplacement extracts old owner, old lease, and new lease.
func parseBinClaimedReplacement(t *testing.T, msg string) (oldOwner, oldLease, newLease string) {
	t.Helper()
	m := binClaimedRe.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("claimed message does not match replacement format: %q", msg)
	}
	return m[1], m[2], m[3]
}

// TestBinary_ForceClaimAuditFields verifies through the real binary that a
// forced claim audits the true prior owner/lease/state (FA1): active, expired,
// and legacy records all report their pre-mutation values, and the old fields
// are never aliased to the new owner or the new lease-id.
func TestBinary_ForceClaimAuditFields(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		if r := runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m"); r.code != 0 {
			t.Fatalf("claim failed: %d\n%s", r.code, r.stderr)
		}
		oldID := readBinLeaseID(t, dir, "TASK-001")

		if r := runBin(t, dir, "claim", "TASK-001", "--agent", "admin", "--force", "--reason", "takeover", "--ttl", "30m"); r.code != 0 {
			t.Fatalf("force claim failed: %d\n%s", r.code, r.stderr)
		}
		newID := readBinLeaseID(t, dir, "TASK-001")
		if newID == oldID {
			t.Fatalf("expected a fresh lease id, got %q", newID)
		}

		oldOwner, oldLease, gotNew := parseBinClaimedReplacement(t, readBinClaimedMsg(t, dir, "TASK-001"))
		if oldOwner != "a1" || oldLease != oldID || gotNew != newID {
			t.Errorf("claimed event wrong: owner=%q lease=%q new=%q (want owner=a1 lease=%s new=%s)", oldOwner, oldLease, gotNew, oldID, newID)
		}
		audit := readBinAudit(t, dir, "TASK-001")
		if audit.actor != "admin" || audit.reason != "takeover" || audit.owner != "a1" || audit.leaseID != oldID || audit.state != "active" || audit.outcome != "lease replaced" {
			t.Errorf("audit wrong: %+v", audit)
		}
		if audit.owner == "admin" || audit.leaseID == newID {
			t.Errorf("audit old fields polluted by new values: %+v", audit)
		}
	})

	t.Run("expired", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		writeExpiredLease(t, dir, "TASK-001", "a1", "lease-old")
		if r := runBin(t, dir, "claim", "TASK-001", "--agent", "admin", "--force", "--reason", "recover", "--ttl", "30m"); r.code != 0 {
			t.Fatalf("force claim over expired failed: %d\n%s", r.code, r.stderr)
		}
		audit := readBinAudit(t, dir, "TASK-001")
		if audit.owner != "a1" || audit.leaseID != "lease-old" || audit.state != "expired" || audit.outcome != "lease replaced" {
			t.Errorf("audit wrong: %+v", audit)
		}
	})

	t.Run("legacy", func(t *testing.T) {
		dir := initBinDir(t)
		addBinTask(t, dir)
		writeBinLegacyLock(t, dir, "TASK-001", "old-agent")
		if r := runBin(t, dir, "claim", "TASK-001", "--agent", "admin", "--force", "--reason", "migrate", "--ttl", "30m"); r.code != 0 {
			t.Fatalf("force claim over legacy failed: %d\n%s", r.code, r.stderr)
		}
		newID := readBinLeaseID(t, dir, "TASK-001")
		audit := readBinAudit(t, dir, "TASK-001")
		if audit.owner != "old-agent" || audit.leaseID != "" || audit.state != "legacy" {
			t.Errorf("audit wrong: %+v", audit)
		}
		if audit.leaseID == newID {
			t.Errorf("legacy audit must not claim the new lease as old: %+v", audit)
		}
		oldOwner, oldLease, gotNew := parseBinClaimedReplacement(t, readBinClaimedMsg(t, dir, "TASK-001"))
		if oldOwner != "old-agent" || oldLease != "" || gotNew != newID {
			t.Errorf("claimed event wrong: owner=%q lease=%q new=%q", oldOwner, oldLease, gotNew)
		}
	})
}

// TestBinary_ForceReleaseAuditFields verifies forced release through the real
// binary audits full prior fields for active/expired/legacy and removes only
// the target lease (FA2).
func TestBinary_ForceReleaseAuditFields(t *testing.T) {
	cases := []struct {
		name      string
		plant     func(t *testing.T, dir string)
		owner     string
		wantLease func(t *testing.T, dir string) string
		state     string
	}{
		{"active", func(t *testing.T, dir string) {
			if r := runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m"); r.code != 0 {
				t.Fatalf("claim failed: %d", r.code)
			}
		}, "a1", func(t *testing.T, dir string) string { return readBinLeaseID(t, dir, "TASK-001") }, "active"},
		{"expired", func(t *testing.T, dir string) { writeExpiredLease(t, dir, "TASK-001", "a1", "lease-old") }, "a1", func(t *testing.T, dir string) string { return "lease-old" }, "expired"},
		{"legacy", func(t *testing.T, dir string) { writeBinLegacyLock(t, dir, "TASK-001", "old-agent") }, "old-agent", func(t *testing.T, dir string) string { return "" }, "legacy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := initBinDir(t)
			addBinTask(t, dir)
			tc.plant(t, dir)
			// Capture the prior lease-id BEFORE the release removes it.
			wantLease := tc.wantLease(t, dir)

			r := runBin(t, dir, "release", "TASK-001", "--agent", "admin", "--force", "--reason", "break")
			if r.code != 0 {
				t.Fatalf("forced release failed: %d\n%s", r.code, r.stderr)
			}
			if binHasLock(t, dir, "TASK-001") {
				t.Error("forced release did not remove the target lease")
			}
			audit := readBinAudit(t, dir, "TASK-001")
			if audit.actor != "admin" || audit.reason != "break" ||
				audit.owner != tc.owner || audit.leaseID != wantLease || audit.state != tc.state ||
				audit.outcome != "lease removed" {
				t.Errorf("audit wrong: %+v (want owner=%s lease=%q state=%s)", audit, tc.owner, wantLease, tc.state)
			}
		})
	}
}

// TestBinary_ForceDoneAuditFields verifies forced completion through the real
// binary audits full prior fields for active/expired/legacy, completes the
// task, and removes only the target lease (FA2).
func TestBinary_ForceDoneAuditFields(t *testing.T) {
	cases := []struct {
		name      string
		plant     func(t *testing.T, dir string)
		owner     string
		wantLease func(t *testing.T, dir string) string
		state     string
	}{
		{"active", func(t *testing.T, dir string) {
			if r := runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m"); r.code != 0 {
				t.Fatalf("claim failed: %d", r.code)
			}
		}, "a1", func(t *testing.T, dir string) string { return readBinLeaseID(t, dir, "TASK-001") }, "active"},
		{"expired", func(t *testing.T, dir string) { writeExpiredLease(t, dir, "TASK-001", "a1", "lease-old") }, "a1", func(t *testing.T, dir string) string { return "lease-old" }, "expired"},
		{"legacy", func(t *testing.T, dir string) { writeBinLegacyLock(t, dir, "TASK-001", "old-agent") }, "old-agent", func(t *testing.T, dir string) string { return "" }, "legacy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := initBinDir(t)
			addBinTask(t, dir)
			tc.plant(t, dir)
			// Capture the prior lease-id BEFORE the completion removes it.
			wantLease := tc.wantLease(t, dir)

			r := runBin(t, dir, "done", "TASK-001", "--result", "passed", "--agent", "admin", "--force", "--reason", "break")
			if r.code != 0 {
				t.Fatalf("forced done failed: %d\n%s", r.code, r.stderr)
			}
			tl, err := store.NewStore(dir).ReadTasks()
			if err != nil {
				t.Fatalf("ReadTasks failed: %v", err)
			}
			done := false
			for _, task := range tl.Tasks {
				if task.ID == "TASK-001" && task.Status == model.StatusDone {
					done = true
				}
			}
			if !done {
				t.Error("forced done did not complete the task")
			}
			if binHasLock(t, dir, "TASK-001") {
				t.Error("forced done did not remove the target lease")
			}
			audit := readBinAudit(t, dir, "TASK-001")
			if audit.actor != "admin" || audit.reason != "break" ||
				audit.owner != tc.owner || audit.leaseID != wantLease || audit.state != tc.state ||
				audit.outcome != "lease removed" {
				t.Errorf("audit wrong: %+v (want owner=%s lease=%q state=%s)", audit, tc.owner, wantLease, tc.state)
			}
		})
	}
}

// TestBinary_ForceStartBlockNoteAuditFields samples start/block/note through
// the real binary to confirm the shared audit format stays complete and the
// lease is retained.
func TestBinary_ForceStartBlockNoteAuditFields(t *testing.T) {
	for _, args := range [][]string{
		{"start", "TASK-001", "--agent", "admin", "--force", "--reason", "override"},
		{"block", "TASK-001", "blocked", "--agent", "admin", "--force", "--reason", "override"},
		{"note", "TASK-001", "n", "--agent", "admin", "--force", "--reason", "override"},
	} {
		t.Run(args[0], func(t *testing.T) {
			dir := initBinDir(t)
			addBinTask(t, dir)
			if r := runBin(t, dir, "claim", "TASK-001", "--agent", "a1", "--ttl", "30m"); r.code != 0 {
				t.Fatalf("claim failed: %d", r.code)
			}
			leaseID := readBinLeaseID(t, dir, "TASK-001")

			if r := runBin(t, dir, args...); r.code != 0 {
				t.Fatalf("%s failed: %d\n%s", args[0], r.code, r.stderr)
			}
			audit := readBinAudit(t, dir, "TASK-001")
			if audit.actor != "admin" || audit.reason != "override" ||
				audit.owner != "a1" || audit.leaseID != leaseID || audit.state != "active" ||
				audit.outcome != "lease retained" {
				t.Errorf("audit wrong: %+v", audit)
			}
			if !binHasLock(t, dir, "TASK-001") {
				t.Errorf("%s must retain the lease", args[0])
			}
		})
	}
}
