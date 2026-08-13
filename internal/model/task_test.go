package model

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTaskUnmarshalYAML_NewEvidenceField(t *testing.T) {
	data := "id: TASK-001\ntitle: Test\nstatus: done\nevidence:\n  - evidence/TASK-001.md\n"
	var task Task
	if err := yaml.Unmarshal([]byte(data), &task); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(task.Evidence) != 1 || task.Evidence[0] != "evidence/TASK-001.md" {
		t.Errorf("expected [evidence/TASK-001.md], got %v", task.Evidence)
	}
}

func TestTaskUnmarshalYAML_OldEvidencePathField(t *testing.T) {
	data := "id: TASK-001\ntitle: Test\nstatus: done\nevidence_path: evidence/TASK-001.md\n"
	var task Task
	if err := yaml.Unmarshal([]byte(data), &task); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(task.Evidence) != 1 || task.Evidence[0] != "evidence/TASK-001.md" {
		t.Errorf("expected [evidence/TASK-001.md] from old evidence_path, got %v", task.Evidence)
	}
}

func TestTaskUnmarshalYAML_NoEvidence(t *testing.T) {
	data := "id: TASK-001\ntitle: Test\nstatus: pending\n"
	var task Task
	if err := yaml.Unmarshal([]byte(data), &task); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(task.Evidence) != 0 {
		t.Errorf("expected empty evidence, got %v", task.Evidence)
	}
}

func TestTaskMarshalYAML_EvidenceSlice(t *testing.T) {
	task := Task{
		ID:       "TASK-001",
		Title:    "Test task",
		Status:   "done",
		Evidence: []string{"evidence/TASK-001.md"},
	}

	data, err := yaml.Marshal(task)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	output := string(data)
	if !strings.Contains(output, "evidence:") {
		t.Errorf("expected marshaled YAML to contain 'evidence:', got:\n%s", output)
	}
	if strings.Contains(output, "evidence_path:") {
		t.Errorf("expected no 'evidence_path:' in marshaled YAML, got:\n%s", output)
	}
}
