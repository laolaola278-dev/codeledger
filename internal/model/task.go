package model

import (
	"gopkg.in/yaml.v3"
)

type TaskTest struct {
	Command string `yaml:"command" json:"command"`
	Result  string `yaml:"result" json:"result"`
}

type Task struct {
	ID            string   `yaml:"id" json:"id"`
	Title         string   `yaml:"title" json:"title"`
	Description   string   `yaml:"description" json:"description"`
	Status        string   `yaml:"status" json:"status"`
	Priority      string   `yaml:"priority" json:"priority"`
	DependsOn     []string `yaml:"depends_on" json:"depends_on"`
	Files         []string `yaml:"files" json:"files"`
	Notes         string   `yaml:"notes" json:"notes"`
	BlockedReason string   `yaml:"blocked_reason" json:"blocked_reason"`
	Test          TaskTest `yaml:"test" json:"test"`
	Evidence      []string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
	CreatedAt     string   `yaml:"created_at" json:"created_at"`
	UpdatedAt     string   `yaml:"updated_at" json:"updated_at"`
	CompletedAt   string   `yaml:"completed_at" json:"completed_at"`
}

type TaskList struct {
	Tasks []Task `yaml:"tasks" json:"tasks"`
}

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusBlocked    = "blocked"
)

const (
	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
)

const (
	TestResultPassed  = "passed"
	TestResultFailed  = "failed"
	TestResultSkipped = "skipped"
	TestResultUnknown = "unknown"
)

func IsValidStatus(s string) bool {
	switch s {
	case StatusPending, StatusInProgress, StatusDone, StatusBlocked:
		return true
	}
	return false
}

func IsValidPriority(p string) bool {
	switch p {
	case PriorityLow, PriorityMedium, PriorityHigh:
		return true
	}
	return false
}

func IsValidTestResult(r string) bool {
	switch r {
	case TestResultPassed, TestResultFailed, TestResultSkipped, TestResultUnknown:
		return true
	}
	return false
}

// UnmarshalYAML handles backward compatibility with the old evidence_path field.
func (t *Task) UnmarshalYAML(value *yaml.Node) error {
	type rawTask struct {
		ID            string   `yaml:"id"`
		Title         string   `yaml:"title"`
		Description   string   `yaml:"description"`
		Status        string   `yaml:"status"`
		Priority      string   `yaml:"priority"`
		DependsOn     []string `yaml:"depends_on"`
		Files         []string `yaml:"files"`
		Notes         string   `yaml:"notes"`
		BlockedReason string   `yaml:"blocked_reason"`
		Test          TaskTest `yaml:"test"`
		Evidence      []string `yaml:"evidence"`
		EvidencePath  string   `yaml:"evidence_path"`
		CreatedAt     string   `yaml:"created_at"`
		UpdatedAt     string   `yaml:"updated_at"`
		CompletedAt   string   `yaml:"completed_at"`
	}

	var raw rawTask
	if err := value.Decode(&raw); err != nil {
		return err
	}

	*t = Task{
		ID:            raw.ID,
		Title:         raw.Title,
		Description:   raw.Description,
		Status:        raw.Status,
		Priority:      raw.Priority,
		DependsOn:     raw.DependsOn,
		Files:         raw.Files,
		Notes:         raw.Notes,
		BlockedReason: raw.BlockedReason,
		Test:          raw.Test,
		CreatedAt:     raw.CreatedAt,
		UpdatedAt:     raw.UpdatedAt,
		CompletedAt:   raw.CompletedAt,
	}

	if len(raw.Evidence) > 0 {
		t.Evidence = raw.Evidence
	} else if raw.EvidencePath != "" {
		t.Evidence = []string{raw.EvidencePath}
	}

	return nil
}
