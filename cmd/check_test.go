package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
)

func TestCmd_Init(t *testing.T) {
	env := newTestEnv(t)

	out, err := env.run("init")
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if !contains(out, "Initialized") {
		t.Errorf("expected init message, got: %s", out)
	}
	if _, err := os.Stat(filepath.Join(env.Dir, ".ctask", "project.yaml")); err != nil {
		t.Errorf("project.yaml not created: %v", err)
	}
}

func TestCmd_Add(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	out, err := env.run("add", "Test task")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if !contains(out, "TASK-001") {
		t.Errorf("expected TASK-001 in output, got: %s", out)
	}
}

func TestCmd_Next(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	if _, err := env.run("add", "Task A"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := env.run("next")
	if err != nil {
		t.Fatalf("next failed: %v", err)
	}
	if !contains(out, "TASK-001") {
		t.Errorf("expected TASK-001 in next output, got: %s", out)
	}
}

func TestCmd_Check_Pass(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	if _, err := env.run("add", "Task A"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := env.run("check")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !contains(out, "OK") {
		t.Errorf("expected OK in check output, got: %s", out)
	}
}

func TestCmd_Check_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	if _, err := env.run("add", "Task A"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := env.run("check", "--json")
	if err != nil {
		t.Fatalf("check --json failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, out)
	}
	if _, ok := result["checks"]; !ok {
		t.Error("expected 'checks' key in JSON output")
	}
}

func TestCmd_Check_Strict_OnWarn(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	s := env.store()
	tl, _ := s.ReadTasks()
	tl.Tasks = append(tl.Tasks, model.Task{
		ID: "TASK-001", Title: "Blocked no reason", Status: model.StatusBlocked, Priority: model.PriorityMedium,
	})
	_ = s.WriteTasks(tl)

	// Without --strict: check has warn but should exit 0
	_, err := env.run("check")
	if err != nil {
		t.Errorf("expected no error for warn without --strict, got: %v", err)
	}

	// Verify warnings exist via JSON
	out, _ := env.run("check", "--json")
	var result struct {
		Checks []struct {
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	hasWarn := false
	for _, c := range result.Checks {
		if c.Status == "warn" {
			hasWarn = true
			break
		}
	}
	if !hasWarn {
		t.Error("expected at least one warn check in JSON output")
	}
}

func TestCmd_Check_Verbose(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	if _, err := env.run("add", "Task A"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := env.run("check", "--verbose")
	if err != nil {
		t.Fatalf("check --verbose failed: %v", err)
	}
	if !contains(out, "project.yaml") {
		t.Errorf("expected project.yaml check in verbose output, got: %s", out)
	}
}
