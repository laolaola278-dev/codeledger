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
	// ErrLeaseConflict is wrapped when an operation collides with a lease
	// held by another owner, or a claim is blocked by an active lease.
	ErrLeaseConflict = errors.New("lease conflict")
	// ErrLeaseExpired is wrapped when an operation requires a valid lease
	// but the lease has expired (e.g. heartbeat on an expired lease).
	ErrLeaseExpired = errors.New("lease expired")
	// ErrForceRequired is wrapped when breaking a lease requires --force
	// with an explicit --reason and the request is missing one of them.
	ErrForceRequired = errors.New("force required")
	// ErrLeaseNotFound is wrapped when a lease operation targets a task with
	// no lease at all.
	ErrLeaseNotFound = errors.New("lease not found")
	// ErrLegacyState is wrapped when a pre-P1 lock entry (no lease_id, or
	// unparseable fields) is encountered. Legacy state is fail-closed: it
	// blocks claims and cannot be renewed or released without an explicit
	// --force --reason.
	ErrLegacyState = errors.New("legacy lease state")
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

// ClaimTask claims a task for an agent by creating a lease. It validates that
// the task is not done or blocked, dependencies are met, and no active lease
// exists. On success it returns the created lease (including its lease_id).
//
// Lease contract:
//   - every claim gets a unique lease_id (from newID);
//   - the lease duration is parsed from ttl and recorded as lease_duration;
//   - an active lease held by another agent blocks the claim (LEASE_CONFLICT);
//   - an unexpired legacy lock blocks the claim fail-closed (LEGACY_STATE);
//   - expired entries (valid or legacy) are cleaned during the claim.
func ClaimTask(s *store.Store, clk clock.Clock, newID lease.IDGen, taskID, agent, role, ttl string) (*model.Lock, error) {
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

	// Check locks and clean expired ones
	locks, err := s.ReadLocks()
	if err != nil {
		return nil, err
	}

	now := clk.Now()
	for _, l := range locks.Locks {
		if l.TaskID != taskID || l.ExpiredAt(now) {
			continue
		}
		if l.Legacy() {
			return nil, fmt.Errorf("%w: task %s has a legacy lock from %s; clear it with 'ctask release %s --force --reason \"<reason>\"'",
				ErrLegacyState, taskID, l.Agent, taskID)
		}
		return nil, fmt.Errorf("%w: task %s is already claimed by %s until %s (lease %s)",
			ErrLeaseConflict, taskID, l.Agent, l.ExpiresAt, l.LeaseID)
	}

	// Parse the lease duration (TTL).
	duration, err := time.ParseDuration(ttl)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (use format like 120m, 2h)", ErrInvalidTTL, ttl)
	}
	if duration <= 0 {
		return nil, fmt.Errorf("%w: %s (must be a positive duration like 120m, 2h)", ErrInvalidTTL, ttl)
	}

	acquiredAt := now.UTC().Format(time.RFC3339)
	expiresAt := now.UTC().Add(duration).Format(time.RFC3339)

	lock := model.Lock{
		TaskID:        taskID,
		Agent:         agent,
		Role:          role,
		LeaseID:       newID(),
		LeaseDuration: ttl,
		AcquiredAt:    acquiredAt,
		ExpiresAt:     expiresAt,
		HeartbeatAt:   acquiredAt,
	}

	// Filter out expired locks, add new lease
	var activeLocks []model.Lock
	for _, l := range locks.Locks {
		if !l.ExpiredAt(now) {
			activeLocks = append(activeLocks, l)
		}
	}
	activeLocks = append(activeLocks, lock)
	locks.Locks = activeLocks

	if err := s.WriteLocks(locks); err != nil {
		return nil, err
	}

	// Update task status to in_progress
	task.Status = model.StatusInProgress
	task.UpdatedAt = now.UTC().Format(time.RFC3339)
	if err := s.WriteTasks(tl); err != nil {
		return nil, err
	}

	// Log event
	evt := model.NewEvent(model.EventTaskClaimed, taskID, task.Title, fmt.Sprintf("lease %s expires %s", lock.LeaseID, lock.ExpiresAt))
	evt.Agent = agent
	evt.Role = role
	if err := s.AppendEvent(evt); err != nil {
		return nil, err
	}

	return &lock, nil
}

// ReleaseTask releases a lease on a task. If the task is in_progress, it is
// set back to pending.
//
// Strict owner/lease contract:
//   - an active lease can only be released by its owner (--agent matching, and
//     --lease-id matching when provided), or by force;
//   - --force requires an explicit --reason (FORCE_REQUIRED otherwise);
//   - a legacy lock cannot be released without --force --reason (fail-closed);
//   - an expired lease is stale and can be cleaned by anyone without force.
//
// A forced release records a task.lease_broken event with the reason; a normal
// release records task.released.
func ReleaseTask(s *store.Store, clk clock.Clock, taskID, agent, leaseID string, force bool, reason string) error {
	if force && reason == "" {
		return fmt.Errorf("%w: --force requires --reason", ErrForceRequired)
	}

	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}

	now := clk.Now()

	// Find the target lease for the task.
	var target *model.Lock
	for i := range locks.Locks {
		if locks.Locks[i].TaskID == taskID {
			target = &locks.Locks[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("%w: task %s has no active lease", ErrLeaseNotFound, taskID)
	}

	broken := false
	switch {
	case target.Legacy():
		if !force {
			return fmt.Errorf("%w: task %s has a legacy lock from %s; pass --force --reason to remove it",
				ErrLegacyState, taskID, target.Agent)
		}
		broken = true
	case target.ExpiredAt(now):
		// Stale lease: clean it without ownership proof.
	case agent == "":
		return fmt.Errorf("%w: task %s is leased by %s; pass --agent %s (or --force --reason)",
			ErrLeaseConflict, taskID, target.Agent, target.Agent)
	case target.Agent != agent:
		if !force {
			return fmt.Errorf("%w: task %s is leased by %s, not %s (use --force --reason to break it)",
				ErrLeaseConflict, taskID, target.Agent, agent)
		}
		broken = true
	case leaseID != "" && target.LeaseID != leaseID:
		return fmt.Errorf("%w: task %s lease %s does not match %s", ErrLeaseConflict, taskID, target.LeaseID, leaseID)
	}

	// Remove the lease.
	var activeLocks []model.Lock
	for _, l := range locks.Locks {
		if l.TaskID != taskID {
			activeLocks = append(activeLocks, l)
		}
	}
	locks.Locks = activeLocks
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
		task.UpdatedAt = now.UTC().Format(time.RFC3339)
		tl.Tasks[idx] = *task
		if err := s.WriteTasks(tl); err != nil {
			return err
		}
	}

	if broken {
		evt := model.NewEvent(model.EventTaskLeaseBroken, taskID, task.Title, reason)
		evt.Agent = agent
		return s.AppendEvent(evt)
	}

	evt := model.NewEvent(model.EventTaskReleased, taskID, task.Title, agent)
	evt.Agent = agent
	return s.AppendEvent(evt)
}

// HeartbeatTask renews a lease for a task lock: it extends ExpiresAt by the
// full recorded lease duration (a true renewal, not a liveness stamp) and
// updates HeartbeatAt. It returns the updated lease.
//
// Strict owner/lease contract:
//   - the lease must be valid (not legacy) - legacy state is fail-closed;
//   - an expired lease cannot be renewed (LEASE_EXPIRED); re-claim instead;
//   - only the lease owner may renew (agent match; lease_id when provided).
func HeartbeatTask(s *store.Store, clk clock.Clock, taskID, agent, leaseID string) (*model.Lock, error) {
	locks, err := s.ReadLocks()
	if err != nil {
		return nil, err
	}

	now := clk.Now()

	var target *model.Lock
	for i := range locks.Locks {
		if locks.Locks[i].TaskID == taskID {
			target = &locks.Locks[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: task %s has no active lease", ErrLeaseNotFound, taskID)
	}

	if target.Legacy() {
		return nil, fmt.Errorf("%w: task %s has a legacy lock that cannot be renewed; release it with --force --reason and claim again",
			ErrLegacyState, taskID)
	}
	if target.ExpiredAt(now) {
		return nil, fmt.Errorf("%w: lease for task %s expired at %s; claim the task again",
			ErrLeaseExpired, taskID, target.ExpiresAt)
	}
	if target.Agent != agent {
		return nil, fmt.Errorf("%w: task %s is leased by %s, not %s",
			ErrLeaseConflict, taskID, target.Agent, agent)
	}
	if leaseID != "" && target.LeaseID != leaseID {
		return nil, fmt.Errorf("%w: task %s lease %s does not match %s",
			ErrLeaseConflict, taskID, target.LeaseID, leaseID)
	}

	// True renewal: extend by the full recorded lease duration.
	duration, err := time.ParseDuration(target.LeaseDuration)
	if err != nil {
		return nil, fmt.Errorf("%w: task %s has an unparseable lease duration %q; release with --force --reason and claim again",
			ErrLegacyState, taskID, target.LeaseDuration)
	}

	newExpiry := now.UTC().Add(duration)
	target.ExpiresAt = newExpiry.Format(time.RFC3339)
	target.HeartbeatAt = now.UTC().Format(time.RFC3339)

	if err := s.WriteLocks(locks); err != nil {
		return nil, err
	}

	evt := model.NewEvent(model.EventTaskHeartbeat, taskID, "", fmt.Sprintf("heartbeat received for lease %s", target.LeaseID))
	evt.Agent = agent
	if err := s.AppendEvent(evt); err != nil {
		return nil, err
	}
	renewedEvt := model.NewEvent(model.EventTaskLeaseRenewed, taskID, "", fmt.Sprintf("lease renewed until %s", target.ExpiresAt))
	renewedEvt.Agent = agent
	if err := s.AppendEvent(renewedEvt); err != nil {
		return nil, err
	}

	return target, nil
}
