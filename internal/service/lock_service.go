package service

import (
	"fmt"
	"sort"
	"time"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
)

// NextTaskResult holds the result of NextTask.
type NextTaskResult struct {
	Task      *model.Task `json:"task,omitempty"`
	Available bool        `json:"available"`
	Message   string      `json:"message,omitempty"`
}

// NextTask finds the next available task to work on.
// It considers task status, dependency completion, and active locks.
// Tasks are sorted by priority (high > medium > low), then created_at.
func NextTask(s *store.Store, role string) (*NextTaskResult, error) {
	tl, err := s.ReadTasks()
	if err != nil {
		return nil, err
	}

	locks, err := s.ReadLocks()
	if err != nil {
		return nil, err
	}

	// Build set of locked task IDs (non-expired only)
	lockedTasks := make(map[string]bool)
	for _, l := range locks.Locks {
		if !l.IsExpired() {
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

// ClaimTask claims a task for an agent. It validates that the task is not
// done or blocked, dependencies are met, and no active lock exists.
func ClaimTask(s *store.Store, taskID, agent, role, ttl string) error {
	tl, err := s.ReadTasks()
	if err != nil {
		return err
	}

	_, task, err := findTaskByID(tl, taskID)
	if err != nil {
		return err
	}

	if task.Status == model.StatusDone {
		return fmt.Errorf("cannot claim task %s: it is already completed", taskID)
	}

	if task.Status == model.StatusBlocked {
		return fmt.Errorf("cannot claim task %s: it is blocked", taskID)
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
			return fmt.Errorf("cannot claim task %s: dependency %s is not completed", taskID, depID)
		}
	}

	// Check locks and clean expired ones
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}

	now := time.Now()
	for _, l := range locks.Locks {
		if l.TaskID == taskID && !l.IsExpired() {
			return fmt.Errorf("task %s is already claimed by %s until %s", taskID, l.Agent, l.ExpiresAt)
		}
	}

	// Parse TTL
	duration, err := time.ParseDuration(ttl)
	if err != nil {
		return fmt.Errorf("invalid ttl: %s (use format like 120m, 2h)", ttl)
	}

	acquiredAt := now.Format(time.RFC3339)
	expiresAt := now.Add(duration).Format(time.RFC3339)

	lock := model.Lock{
		TaskID:      taskID,
		Agent:       agent,
		Role:        role,
		AcquiredAt:  acquiredAt,
		ExpiresAt:   expiresAt,
		HeartbeatAt: acquiredAt,
	}

	// Filter out expired locks, add new lock
	var activeLocks []model.Lock
	for _, l := range locks.Locks {
		if !l.IsExpired() {
			activeLocks = append(activeLocks, l)
		}
	}
	activeLocks = append(activeLocks, lock)
	locks.Locks = activeLocks

	if err := s.WriteLocks(locks); err != nil {
		return err
	}

	// Update task status to in_progress
	task.Status = model.StatusInProgress
	task.UpdatedAt = util.NowRFC3339()
	if err := s.WriteTasks(tl); err != nil {
		return err
	}

	// Log event
	evt := model.NewEvent(model.EventTaskClaimed, taskID, task.Title, "")
	evt.Agent = agent
	evt.Role = role
	return s.AppendEvent(evt)
}

// ReleaseTask releases a lock on a task. If the task is in_progress, it is
// set back to pending. If agent is specified, only that agent's lock is released.
func ReleaseTask(s *store.Store, taskID, agent string) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}

	// Find and remove the lock
	found := false
	var activeLocks []model.Lock
	for _, l := range locks.Locks {
		if l.TaskID == taskID {
			if agent != "" && l.Agent != agent {
				activeLocks = append(activeLocks, l)
				continue
			}
			found = true
			continue
		}
		activeLocks = append(activeLocks, l)
	}
	locks.Locks = activeLocks

	if !found {
		return fmt.Errorf("task %s has no active lock", taskID)
	}

	if err := s.WriteLocks(locks); err != nil {
		return err
	}

	// If task is in_progress, set back to pending
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
		task.UpdatedAt = util.NowRFC3339()
		tl.Tasks[idx] = *task
		if err := s.WriteTasks(tl); err != nil {
			return err
		}
	}

	evt := model.NewEvent(model.EventTaskReleased, taskID, task.Title, agent)
	evt.Agent = agent
	return s.AppendEvent(evt)
}

// HeartbeatTask updates the heartbeat timestamp for a task lock.
func HeartbeatTask(s *store.Store, taskID, agent string) error {
	locks, err := s.ReadLocks()
	if err != nil {
		return err
	}

	found := false
	for i, l := range locks.Locks {
		if l.TaskID == taskID {
			if agent != "" && l.Agent != agent {
				return fmt.Errorf("task %s is claimed by %s, not %s", taskID, l.Agent, agent)
			}
			locks.Locks[i].HeartbeatAt = time.Now().Format(time.RFC3339)
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("task %s has no active lock", taskID)
	}

	if err := s.WriteLocks(locks); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventTaskHeartbeat, taskID, "", agent)
	evt.Agent = agent
	return s.AppendEvent(evt)
}
