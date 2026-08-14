package service

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// Sentinel errors for the task lease contract. The CLI layer maps these to
// stable machine codes with errors.Is - never by string matching.
var (
	// ErrLeaseConflict is wrapped when a new claim is blocked by an active
	// (non-expired, non-legacy) lease held by any agent, including the same
	// agent (contention, exit 3).
	ErrLeaseConflict = errors.New("lease conflict")
	// ErrLeaseRequired is wrapped when a lock record exists for the target
	// task but the caller supplied neither (or only one of) --agent and
	// --lease-id. Both are mandatory fencing credentials once a record exists.
	ErrLeaseRequired = errors.New("lease credentials required")
	// ErrLeaseMismatch is wrapped when --agent or --lease-id does not match
	// the active lease (wrong owner or wrong fencing token).
	ErrLeaseMismatch = errors.New("lease mismatch")
	// ErrLeaseExpired is wrapped when an ordinary (non-force) operation
	// targets an expired lease. Expired entries are fail-closed: they are
	// never silently cleaned by unrelated operations.
	ErrLeaseExpired = errors.New("lease expired")
	// ErrLeaseNotFound is wrapped when a release/heartbeat targets a task
	// with no lock record at all.
	ErrLeaseNotFound = errors.New("lease not found")
	// ErrLegacyLease is wrapped when an ordinary operation targets a pre-P1
	// legacy lock entry. Legacy state is fail-closed and requires an explicit
	// --force --reason --agent takeover (classified exit 3).
	ErrLegacyLease = errors.New("legacy lease requires takeover")
	// ErrForceReasonRequired is wrapped when --force is used without a
	// non-empty --reason (validation, exit 2).
	ErrForceReasonRequired = errors.New("force requires reason")
	// ErrForceAgentRequired is wrapped when --force is used without a
	// non-empty --agent actor (validation, exit 2).
	ErrForceAgentRequired = errors.New("force requires actor agent")
	// ErrInvalidTTL is wrapped when a lease duration cannot be parsed.
	ErrInvalidTTL = errors.New("invalid ttl")
)

// NextTaskResult holds the result of NextTask.
type NextTaskResult struct {
	Task      *model.Task `json:"task,omitempty"`
	Available bool        `json:"available"`
	Message   string      `json:"message,omitempty"`
}

// NextTask finds the next available task to work on.
// It considers task status, dependency completion, and active leases.
// Tasks are sorted by priority (high > medium > low), then created_at.
// Legacy locks that have not expired are treated as active (fail-closed) so
// a task they reference is never suggested until the legacy state is cleared.
func NextTask(s *store.Store, role string, clk clock.Clock) (*NextTaskResult, error) {
	now := clk.Now()

	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	locks, err := s.ReadLocks()
	if err != nil {
		return nil, err
	}

	// Build set of locked task IDs (active leases only, legacy included).
	lockedTasks := make(map[string]bool)
	for _, l := range locks.Locks {
		if l.IsActiveAt(now) {
			lockedTasks[l.TaskID] = true
		}
	}

	// Build set of done task IDs
	doneSet := make(map[string]bool)
	for _, t := range tl.Tasks {
		if t.Status == model.StatusDone {
			doneSet[t.ID] = true
		}
	}

	// Filter candidates: pending, deps done, not locked
	var candidates []model.Task
	for _, t := range tl.Tasks {
		if t.Status != model.StatusPending {
			continue
		}
		if lockedTasks[t.ID] {
			continue
		}
		allDepsDone := true
		for _, depID := range t.DependsOn {
			if !doneSet[depID] {
				allDepsDone = false
				break
			}
		}
		if !allDepsDone {
			continue
		}
		candidates = append(candidates, t)
	}

	if len(candidates) == 0 {
		return &NextTaskResult{
			Available: false,
			Message:   "No available tasks to work on.",
		}, nil
	}

	// Sort: priority (high > medium > low), then created_at (earlier first)
	sort.Slice(candidates, func(i, j int) bool {
		pi := priorityScore(candidates[i].Priority)
		pj := priorityScore(candidates[j].Priority)
		if pi != pj {
			return pi > pj
		}
		return candidates[i].CreatedAt < candidates[j].CreatedAt
	})

	task := candidates[0]
	return &NextTaskResult{
		Task:      &task,
		Available: true,
	}, nil
}

func priorityScore(p string) int {
	switch p {
	case model.PriorityHigh:
		return 3
	case model.PriorityMedium:
		return 2
	case model.PriorityLow:
		return 1
	}
	return 0
}

// findTaskLock returns the lock entry for taskID, or nil when the task has
// no record. It never mutates the list.
func findTaskLock(locks *model.LockList, taskID string) *model.Lock {
	for i := range locks.Locks {
		if locks.Locks[i].TaskID == taskID {
			return &locks.Locks[i]
		}
	}
	return nil
}

// validateForce enforces the force-override validation contract. It runs
// BEFORE any ownership/state decision: --force always requires a non-empty
// --reason and a non-empty --agent actor, and any missing piece is a typed
// validation failure (exit 2), never a lease conflict.
func validateForce(auth lease.Auth) error {
	if !auth.Force {
		return nil
	}
	if auth.Reason == "" {
		return fmt.Errorf("%w: --force requires a non-empty --reason", ErrForceReasonRequired)
	}
	if auth.Agent == "" {
		return fmt.Errorf("%w: --force requires a non-empty --agent actor", ErrForceAgentRequired)
	}
	return nil
}

// gateResult is the outcome of lease authorization for a protected mutation.
type gateResult struct {
	// entry is the task's existing lock record, or nil when there is none.
	entry *model.Lock
	// forced is true when the caller is authorized via an explicit --force
	// override rather than as the exact lease owner.
	forced bool
}

// gateLease is the single transport-neutral authorization point for every
// protected mutation (heartbeat, release, start, done, block, note).
//
// Deterministic precedence (never string-matched at the process boundary):
//
//  1. --force: validate non-empty reason and actor (validation, exit 2).
//  2. no record: compatible path (notFound=false) or lease-not-found
//     (notFound=true, for release/heartbeat).
//  3. legacy record: fail closed (LEGACY_LEASE_REQUIRES_TAKEOVER, exit 3).
//  4. expired record: fail closed (LEASE_EXPIRED, exit 3).
//  5. active record: both --agent and --lease-id required (LEASE_REQUIRED,
//     exit 3) and must match exactly (LEASE_MISMATCH, exit 3).
//
// A valid --force (already validated above) short-circuits steps 3-5 and
// returns an authorized override for the existing record.
func gateLease(locks *model.LockList, now time.Time, taskID string, auth lease.Auth, notFound bool) (gateResult, error) {
	if err := validateForce(auth); err != nil {
		return gateResult{}, err
	}

	entry := findTaskLock(locks, taskID)
	if entry == nil {
		if notFound {
			return gateResult{}, fmt.Errorf("%w: task %s has no lease", ErrLeaseNotFound, taskID)
		}
		return gateResult{}, nil
	}

	if auth.Force {
		return gateResult{entry: entry, forced: true}, nil
	}

	switch {
	case entry.Legacy():
		return gateResult{}, fmt.Errorf(
			"%w: task %s has a legacy lock (no valid lease_id); pass --force --reason --agent to take it over",
			ErrLegacyLease, taskID)
	case entry.ExpiredAt(now):
		return gateResult{}, fmt.Errorf(
			"%w: lease for task %s expired at %s; re-claim the task or pass --force --reason --agent",
			ErrLeaseExpired, taskID, entry.ExpiresAt)
	default:
		if auth.Agent == "" || auth.LeaseID == "" {
			return gateResult{}, fmt.Errorf(
				"%w: task %s has an active lease; both --agent and --lease-id are required",
				ErrLeaseRequired, taskID)
		}
		if entry.Agent != auth.Agent || entry.LeaseID != auth.LeaseID {
			return gateResult{}, fmt.Errorf(
				"%w: task %s lease %s is owned by %q; agent %q / lease-id %q do not match",
				ErrLeaseMismatch, taskID, entry.LeaseID, entry.Agent, auth.Agent, auth.LeaseID)
		}
		return gateResult{entry: entry}, nil
	}
}

// ClaimTask claims a task for an agent by creating a lease. It validates that
// the task is not done or blocked, dependencies are met, and no active lease
// exists. On success it returns the created lease (including its lease_id).
//
// Lease contract:
//   - every claim gets a unique lease_id (from newID);
//   - the lease duration is parsed from ttl and recorded as lease_duration;
//   - an active lease blocks the claim (LEASE_CONFLICT, exit 3);
//   - a legacy lock blocks the claim fail-closed (legacy takeover, exit 3)
//     unless --force --reason --agent performs an explicit takeover;
//   - an expired NEW-format lease is replaced in place (same-task re-claim);
//     every OTHER task's entry is preserved untouched;
//   - --force --reason --agent performs an explicit takeover of any existing
//     record, creating a fresh lease with a new lease_id.
func ClaimTask(s *store.Store, clk clock.Clock, newID lease.IDGen, taskID string, auth lease.Auth, role, ttl string) (*model.Lock, error) {
	if err := validateForce(auth); err != nil {
		return nil, err
	}
	if auth.Agent == "" {
		return nil, fmt.Errorf("%w: a claim requires --agent", ErrLeaseRequired)
	}

	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	_, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return nil, err
	}

	if task.Status == model.StatusDone {
		return nil, fmt.Errorf("cannot claim task %s: it is already completed", taskID)
	}

	if task.Status == model.StatusBlocked {
		return nil, fmt.Errorf("cannot claim task %s: it is blocked", taskID)
	}

	// Check dependencies
	doneSet := make(map[string]bool)
	for _, t := range tl.Tasks {
		if t.Status == model.StatusDone {
			doneSet[t.ID] = true
		}
	}
	for _, depID := range task.DependsOn {
		if !doneSet[depID] {
			return nil, fmt.Errorf("cannot claim task %s: dependency %s is not completed", taskID, depID)
		}
	}

	// Parse the lease duration (TTL) before touching locks.
	duration, err := time.ParseDuration(ttl)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (use format like 120m, 2h)", ErrInvalidTTL, ttl)
	}
	if duration <= 0 {
		return nil, fmt.Errorf("%w: %s (must be a positive duration like 120m, 2h)", ErrInvalidTTL, ttl)
	}

	locks, err := s.ReadLocks()
	if err != nil {
		return nil, err
	}

	now := clk.Now()
	existing := findTaskLock(locks, taskID)
	// Immutable snapshot of the prior record BEFORE any replacement, so the
	// claim audit never reads values aliased by replaceTaskLock's in-place
	// write of the same slice element.
	var prior *lockSnapshot
	if existing != nil {
		prior = snapshotLock(existing, now)
	}

	if existing != nil && !auth.Force {
		switch {
		case existing.Legacy():
			return nil, fmt.Errorf(
				"%w: task %s has a legacy lock from %s; pass --force --reason --agent to take it over",
				ErrLegacyLease, taskID, existing.Agent)
		case !existing.ExpiredAt(now):
			return nil, fmt.Errorf("%w: task %s is already claimed by %s until %s (lease %s)",
				ErrLeaseConflict, taskID, existing.Agent, existing.ExpiresAt, existing.LeaseID)
		}
		// Expired NEW-format lease: fall through to same-task re-claim.
	}

	acquiredAt := now.UTC().Format(time.RFC3339)
	expiresAt := now.UTC().Add(duration).Format(time.RFC3339)

	newLock := model.Lock{
		TaskID:        taskID,
		Agent:         auth.Agent,
		Role:          role,
		LeaseID:       newID(),
		LeaseDuration: ttl,
		AcquiredAt:    acquiredAt,
		ExpiresAt:     expiresAt,
		HeartbeatAt:   acquiredAt,
	}

	// Target-only replacement: modify ONLY the target task's entry; every
	// other entry (active, expired, or legacy) is preserved byte-for-byte.
	replaced := replaceTaskLock(locks, taskID, newLock)
	if err := s.WriteLocks(locks); err != nil {
		return nil, err
	}

	// Update task status to in_progress
	task.Status = model.StatusInProgress
	task.UpdatedAt = now.UTC().Format(time.RFC3339)
	if err := s.WriteTasks(tl); err != nil {
		return nil, err
	}

	// Log the claim event, including old->new owner/lease when replacing. The
	// old owner/lease come from the pre-replacement snapshot, never from the
	// in-place-replaced slice element.
	claimMsg := fmt.Sprintf("lease %s expires %s", newLock.LeaseID, newLock.ExpiresAt)
	if replaced {
		oldOwner, oldLease := "", ""
		if prior != nil {
			oldOwner, oldLease = prior.owner, prior.leaseID
		}
		claimMsg = fmt.Sprintf("replaced lease for task %s (old owner %q lease %q) with lease %s expires %s",
			taskID, oldOwner, oldLease, newLock.LeaseID, newLock.ExpiresAt)
	}
	evt := model.NewEvent(model.EventTaskClaimed, taskID, task.Title, claimMsg)
	evt.Agent = auth.Agent
	evt.Role = role
	if err := s.AppendEvent(evt); err != nil {
		return nil, err
	}

	// A forced takeover is additionally audited as a lease-broken event with
	// the full prior owner/lease/state snapshot.
	if auth.Force && replaced {
		if err := recordForcedOverrideAudit(s, taskID, task.Title, auth, prior, "lease replaced"); err != nil {
			return nil, err
		}
	}

	return &newLock, nil
}

// ReleaseTask releases a lease on a task. If the task is in_progress, it is
// set back to pending.
//
// Strict owner/lease contract:
//   - an active lease can only be released by its exact owner (matching
//     --agent AND --lease-id), or by --force --reason --agent;
//   - a legacy lock cannot be released without --force --reason --agent
//     (fail-closed, LEGACY_LEASE_REQUIRES_TAKEOVER, exit 3);
//   - an expired lease is fail-closed too (LEASE_EXPIRED, exit 3): ordinary
//     release never cleans it; re-claim or force takeover instead;
//   - releasing removes ONLY the target task's entry.
//
// A forced release records a task.lease_broken event with the reason; a
// normal release records task.released.
func ReleaseTask(s *store.Store, clk clock.Clock, taskID string, auth lease.Auth) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}

	now := clk.Now()
	res, err := gateLease(locks, now, taskID, auth, true)
	if err != nil {
		return err
	}
	// Immutable snapshot of the prior record BEFORE removal so the forced
	// override audit carries the true previous owner/lease/state.
	prior := snapshotLock(res.entry, now)

	// Remove the lease: only the target task's entry.
	removeTaskLock(locks, taskID)
	if err := s.WriteLocks(locks); err != nil {
		return err
	}

	// If task is in_progress, set back to pending.
	tl, err := s.ReadTasks()
	if err != nil {
		return err
	}
	idx, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return err
	}
	if task.Status == model.StatusInProgress {
		task.Status = model.StatusPending
		task.UpdatedAt = clk.Now().UTC().Format(time.RFC3339)
		tl.Tasks[idx] = *task
		if err := s.WriteTasks(tl); err != nil {
			return err
		}
	}

	if res.forced {
		return recordForcedOverrideAudit(s, taskID, task.Title, auth, prior, "lease removed")
	}

	evt := model.NewEvent(model.EventTaskReleased, taskID, task.Title, auth.Agent)
	evt.Agent = auth.Agent
	return s.AppendEvent(evt)
}

// HeartbeatTask renews a lease for a task lock: it extends ExpiresAt by the
// full recorded lease duration (a true renewal, not a liveness stamp) and
// updates HeartbeatAt. It returns the updated lease.
//
// Strict owner/lease contract:
//   - the lease must be a well-formed active lease (legacy fail-closed, exit
//     3; expired fail-closed, exit 3);
//   - only the exact owner may renew: --agent AND --lease-id must both match.
func HeartbeatTask(s *store.Store, clk clock.Clock, taskID string, auth lease.Auth) (*model.Lock, error) {
	locks, err := s.ReadLocks()
	if err != nil {
		return nil, err
	}

	res, err := gateLease(locks, clk.Now(), taskID, auth, true)
	if err != nil {
		return nil, err
	}
	target := res.entry

	// True renewal: extend by the full recorded lease duration.
	duration, err := time.ParseDuration(target.LeaseDuration)
	if err != nil {
		return nil, fmt.Errorf("%w: task %s has an unparseable lease duration %q; release with --force --reason --agent and claim again",
			ErrLegacyLease, taskID, target.LeaseDuration)
	}

	now := clk.Now()
	newExpiry := now.UTC().Add(duration)
	target.ExpiresAt = newExpiry.Format(time.RFC3339)
	target.HeartbeatAt = now.UTC().Format(time.RFC3339)

	if err := s.WriteLocks(locks); err != nil {
		return nil, err
	}

	evt := model.NewEvent(model.EventTaskHeartbeat, taskID, "", fmt.Sprintf("heartbeat received for lease %s", target.LeaseID))
	evt.Agent = auth.Agent
	if err := s.AppendEvent(evt); err != nil {
		return nil, err
	}
	renewedEvt := model.NewEvent(model.EventTaskLeaseRenewed, taskID, "", fmt.Sprintf("lease renewed until %s", target.ExpiresAt))
	renewedEvt.Agent = auth.Agent
	if err := s.AppendEvent(renewedEvt); err != nil {
		return nil, err
	}

	return target, nil
}

// replaceTaskLock replaces the target task's lock entry with newLock in
// place, preserving every other entry. It reports whether an existing entry
// was replaced (as opposed to appended).
func replaceTaskLock(locks *model.LockList, taskID string, newLock model.Lock) bool {
	for i := range locks.Locks {
		if locks.Locks[i].TaskID == taskID {
			locks.Locks[i] = newLock
			return true
		}
	}
	locks.Locks = append(locks.Locks, newLock)
	return false
}

// removeTaskLock removes ONLY the target task's lock entry, preserving all
// other entries (active, expired, or legacy) untouched.
func removeTaskLock(locks *model.LockList, taskID string) {
	out := locks.Locks[:0]
	for _, l := range locks.Locks {
		if l.TaskID != taskID {
			out = append(out, l)
		}
	}
	locks.Locks = out
}

// lockSnapshot is an immutable copy of a task's lock record taken BEFORE any
// mutation. It exists so force-override audits are built from pre-mutation
// values instead of a pointer that replaceTaskLock/removeTaskLock invalidate
// (reading such a pointer after the in-place write yields the NEW values).
type lockSnapshot struct {
	owner   string
	leaseID string
	state   string // "active", "expired", or "legacy"
}

// snapshotLock copies the audit-relevant fields from entry, classifying the
// prior state under the SAME now used by the operation (legacy takes
// precedence, then expired, then active). A nil entry yields nil.
func snapshotLock(entry *model.Lock, now time.Time) *lockSnapshot {
	if entry == nil {
		return nil
	}
	snap := &lockSnapshot{owner: entry.Agent, leaseID: entry.LeaseID}
	switch {
	case entry.Legacy():
		snap.state = "legacy"
	case entry.ExpiredAt(now):
		snap.state = "expired"
	default:
		snap.state = "active"
	}
	return snap
}

// recordForcedOverrideAudit appends a task.lease_broken audit event recording
// the actor, reason, and the prior owner/lease/state snapshot. outcome
// describes the mutation's effect on the lease ("lease replaced", "lease
// removed", "lease retained"). It is the single message format for every
// --force override; prior fields always come from the immutable snapshot
// taken before the mutation, never from the mutated record.
func recordForcedOverrideAudit(s *store.Store, taskID, title string, auth lease.Auth, prior *lockSnapshot, outcome string) error {
	oldOwner, oldLease, oldState := "", "", "none"
	if prior != nil {
		oldOwner, oldLease, oldState = prior.owner, prior.leaseID, prior.state
	}
	evt := model.NewEvent(model.EventTaskLeaseBroken, taskID, title,
		fmt.Sprintf("forced override by %q (reason: %s); previous owner=%q lease=%q state=%s (%s)",
			auth.Agent, auth.Reason, oldOwner, oldLease, oldState, outcome))
	evt.Agent = auth.Agent
	return s.AppendEvent(evt)
}
