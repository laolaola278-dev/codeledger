package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
)

// chdir changes to a temp dir for the duration of the test and restores cwd on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// initTempProject creates a temp dir, cd's into it, and initializes .ctask.
func initTempProject(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ctask-cmd-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	chdir(t, dir)
	s := store.NewStore(".")
	if err := service.InitProject(s); err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}
	return dir
}

// runRootArgs runs rootCmd with the given args and returns stdout.
func runRootArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	resetAllFlags()

	rootCmd.SetArgs(args)
	execErr := rootCmd.Execute()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String(), execErr
}

func TestCmd_Init(t *testing.T) {
	dir, err := os.MkdirTemp("", "ctask-init-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	chdir(t, dir)

	out, err := runRootArgs(t, "init")
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if !contains(out, "Initialized") {
		t.Errorf("expected init message, got: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ctask", "project.yaml")); err != nil {
		t.Errorf("project.yaml not created: %v", err)
	}
}

func TestCmd_Add(t *testing.T) {
	initTempProject(t)

	out, err := runRootArgs(t, "add", "Test task")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if !contains(out, "TASK-001") {
		t.Errorf("expected TASK-001 in output, got: %s", out)
	}
}

func TestCmd_Next(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "next")
	if err != nil {
		t.Fatalf("next failed: %v", err)
	}
	if !contains(out, "TASK-001") {
		t.Errorf("expected TASK-001 in next output, got: %s", out)
	}
}

func TestCmd_Check_Pass(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "check")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !contains(out, "OK") {
		t.Errorf("expected OK in check output, got: %s", out)
	}
}

func TestCmd_Check_JSON(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "check", "--json")
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
	initTempProject(t)

	s := store.NewStore(".")
	tl, _ := s.ReadTasks()
	tl.Tasks = append(tl.Tasks, model.Task{
		ID: "TASK-001", Title: "Blocked no reason", Status: model.StatusBlocked, Priority: model.PriorityMedium,
	})
	_ = s.WriteTasks(tl)

	// Without --strict: check has warn but should exit 0
	_, err := runRootArgs(t, "check")
	if err != nil {
		t.Errorf("expected no error for warn without --strict, got: %v", err)
	}

	// Verify warnings exist via JSON
	out, _ := runRootArgs(t, "check", "--json")
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
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "check", "--verbose")
	if err != nil {
		t.Fatalf("check --verbose failed: %v", err)
	}
	if !contains(out, "project.yaml") {
		t.Errorf("expected project.yaml check in verbose output, got: %s", out)
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
