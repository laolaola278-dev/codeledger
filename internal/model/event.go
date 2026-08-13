package model

import "time"

type Event struct {
	Time    string `json:"time"`
	Type    string `json:"type"`
	TaskID  string `json:"task_id,omitempty"`
	Title   string `json:"title,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Role    string `json:"role,omitempty"`
	Message string `json:"message,omitempty"`
}

const (
	EventTaskCreated             = "task.created"
	EventTaskStarted             = "task.started"
	EventTaskCompleted           = "task.completed"
	EventTaskBlocked             = "task.blocked"
	EventTaskNoted               = "task.noted"
	EventDecisionAdded           = "decision.added"
	EventTaskClaimed             = "task.claimed"
	EventTaskReleased            = "task.released"
	EventTaskHeartbeat           = "task.heartbeat"
	EventLockReleasedOnDone      = "task.lock_released_on_done"
	EventEvidenceRecorded        = "task.evidence_recorded"
	EventEvidenceAdded           = "evidence.added"
	EventDiffCaptured            = "task.diff_captured"
	EventFilesAttached           = "task.files_attached"
	EventChangedListed           = "changed.listed"
	EventDiffListed              = "diff.listed"
	EventCheckPassed             = "check.passed"
	EventCheckFailed             = "check.failed"
	EventSessionFinished         = "session.finished"
	EventProjectLockAcquired     = "project.lock_acquired"
	EventProjectLockReleased     = "project.lock_released"
	EventProjectLockConflict     = "project.lock_conflict"
	EventProjectLockStaleRemoved = "project.lock_stale_removed"
	EventPlanSaved               = "plan.saved"
)

func NewEvent(eventType, taskID, title, message string) Event {
	return Event{
		Time:    time.Now().UTC().Format(time.RFC3339),
		Type:    eventType,
		TaskID:  taskID,
		Title:   title,
		Message: message,
	}
}
