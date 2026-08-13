package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

func TestCmd_PlanGenerate_PrintsPrompt(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A", "--priority", "high")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "plan", "generate", "--mode", "planning", "--agent", "codex")
	if err != nil {
		t.Fatalf("plan generate failed: %v", err)
	}
	if !contains(out, "# CodeLedger Project Context") {
		t.Errorf("expected project context header, got:\n%s", out)
	}
	if !contains(out, "TASK-001") {
		t.Errorf("expected TASK-001 in prompt, got:\n%s", out)
	}
	if !contains(out, `You are agent "codex"`) {
		t.Errorf("expected agent in prompt, got:\n%s", out)
	}
}

func TestCmd_PlanGenerate_TriageAndBlocked(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	out, err := runRootArgs(t, "plan", "generate", "--mode", "triage")
	if err != nil {
		t.Fatalf("plan generate triage failed: %v", err)
	}
	if !contains(out, "# CodeLedger Quick Triage") {
		t.Errorf("expected triage header, got:\n%s", out)
	}

	out, err = runRootArgs(t, "plan", "generate", "--mode", "blocked")
	if err != nil {
		t.Fatalf("plan generate blocked failed: %v", err)
	}
	if !contains(out, "# CodeLedger Blocked Resolution") {
		t.Errorf("expected blocked header, got:\n%s", out)
	}
}

func TestCmd_PlanGenerate_InvalidMode(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "plan", "generate", "--mode", "bogus")
	if err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestCmd_PlanSave_ThenShow(t *testing.T) {
	initTempProject(t)

	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	input := `PLAN-001
Recommendations:
- TASK-001: start | quick win

Rationale: do the quick win first.`
	out, err := runRootArgs(t, "plan", "save", "PLAN-001", "--input", input)
	if err != nil {
		t.Fatalf("plan save failed: %v", err)
	}
	if !contains(out, "PLAN-001 saved") {
		t.Errorf("expected save message, got: %s", out)
	}

	// verify file on disk
	if _, err := os.Stat(filepath.Join(".ctask", "plans", "PLAN-001.yaml")); err != nil {
		t.Fatalf("plan file not created: %v", err)
	}

	// show
	out, err = runRootArgs(t, "plan", "show", "PLAN-001")
	if err != nil {
		t.Fatalf("plan show failed: %v", err)
	}
	if !contains(out, "TASK-001: start | quick win") {
		t.Errorf("expected recommendation in show output, got:\n%s", out)
	}
	if !contains(out, "do the quick win first") {
		t.Errorf("expected rationale in show output, got:\n%s", out)
	}

	// plan.saved event recorded
	s := store.NewStore(".")
	events, _ := s.ReadEvents()
	found := false
	for _, e := range events {
		if e.Type == model.EventPlanSaved && e.TaskID == "" && e.Title == "PLAN-001" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected plan.saved event")
	}
}

func TestCmd_PlanSave_NextIDAndFileInput(t *testing.T) {
	initTempProject(t)

	// save without ID -> auto PLAN-001
	out, err := runRootArgs(t, "plan", "save", "--input", "TASK-001: start | go")
	if err != nil {
		t.Fatalf("plan save failed: %v", err)
	}
	if !contains(out, "PLAN-001 saved") {
		t.Errorf("expected auto PLAN-001, got: %s", out)
	}

	// save with --file -> PLAN-002
	f, err := os.CreateTemp("", "plan-input-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("TASK-002: review | check")
	f.Close()

	out, err = runRootArgs(t, "plan", "save", "--file", f.Name())
	if err != nil {
		t.Fatalf("plan save --file failed: %v", err)
	}
	if !contains(out, "PLAN-002 saved") {
		t.Errorf("expected PLAN-002, got: %s", out)
	}
}

func TestCmd_PlanSave_PromptRoundTrip(t *testing.T) {
	initTempProject(t)

	_, err := runRootArgs(t, "add", "Task A")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// 1. plan generate -> capture the prompt text
	genOut, err := runRootArgs(t, "plan", "generate", "--mode", "planning", "--agent", "codex")
	if err != nil {
		t.Fatalf("plan generate failed: %v", err)
	}
	promptText := strings.TrimSpace(genOut)
	if promptText == "" {
		t.Fatal("expected non-empty prompt from plan generate")
	}

	// 2. plan save --prompt "<text>"
	out, err := runRootArgs(t, "plan", "save", "PLAN-001", "--input", "TASK-001: start | quick win", "--prompt", promptText)
	if err != nil {
		t.Fatalf("plan save --prompt failed: %v", err)
	}
	if !contains(out, "PLAN-001 saved") {
		t.Errorf("expected save message, got: %s", out)
	}

	// 3. plan show --prompt -> prompt must be echoed back
	showOut, err := runRootArgs(t, "plan", "show", "PLAN-001", "--prompt")
	if err != nil {
		t.Fatalf("plan show --prompt failed: %v", err)
	}
	if !contains(showOut, promptText) {
		t.Errorf("expected prompt to be echoed back, got:\n%s", showOut)
	}
	if contains(showOut, "(no prompt recorded for this plan)") {
		t.Error("expected prompt to be recorded, but got the empty-prompt placeholder")
	}
}

func TestCmd_PlanSave_PromptFromFile(t *testing.T) {
	initTempProject(t)

	promptText := "# CodeLedger Project Context\n\n## Project\nlong prompt stored in a file\n"
	f, err := os.CreateTemp("", "plan-prompt-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(promptText); err != nil {
		t.Fatalf("write prompt file failed: %v", err)
	}
	f.Close()

	out, err := runRootArgs(t, "plan", "save", "PLAN-001", "--input", "TASK-001: start | quick win", "--prompt-file", f.Name())
	if err != nil {
		t.Fatalf("plan save --prompt-file failed: %v", err)
	}
	if !contains(out, "PLAN-001 saved") {
		t.Errorf("expected save message, got: %s", out)
	}

	// prompt-file and prompt are mutually exclusive
	if _, err := runRootArgs(t, "plan", "save", "PLAN-002", "--input", "TASK-001: start | quick win", "--prompt", "x", "--prompt-file", f.Name()); err == nil {
		t.Error("expected error when both --prompt and --prompt-file are given")
	}
}

func TestCmd_PlanSave_Mode(t *testing.T) {
	initTempProject(t)

	_, err := runRootArgs(t, "plan", "save", "PLAN-001", "--input", "TASK-001: start | quick win", "--mode", "triage")
	if err != nil {
		t.Fatalf("plan save --mode failed: %v", err)
	}

	// PLAN-001.yaml must contain mode: triage
	data, err := os.ReadFile(filepath.Join(".ctask", "plans", "PLAN-001.yaml"))
	if err != nil {
		t.Fatalf("failed to read PLAN-001.yaml: %v", err)
	}
	if !strings.Contains(string(data), "mode: triage") {
		t.Errorf("expected mode: triage in PLAN-001.yaml, got:\n%s", string(data))
	}

	// invalid mode is ignored, not an error
	if _, err := runRootArgs(t, "plan", "save", "PLAN-002", "--input", "TASK-001: start | quick win", "--mode", "bogus"); err != nil {
		t.Errorf("invalid mode should be ignored, got error: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(".ctask", "plans", "PLAN-002.yaml"))
	if err != nil {
		t.Fatalf("failed to read PLAN-002.yaml: %v", err)
	}
	if strings.Contains(string(data2), "mode: bogus") {
		t.Errorf("invalid mode should not be written, got:\n%s", string(data2))
	}
}

func TestCmd_PlanSave_NoInput(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "plan", "save")
	if err == nil {
		t.Error("expected error when no input provided")
	}
}

func TestCmd_PlanList(t *testing.T) {
	initTempProject(t)

	_, err := runRootArgs(t, "plan", "save", "--input", "TASK-001: start | a")
	if err != nil {
		t.Fatalf("save 1 failed: %v", err)
	}
	_, err = runRootArgs(t, "plan", "save", "--input", "TASK-002: review | b")
	if err != nil {
		t.Fatalf("save 2 failed: %v", err)
	}

	out, err := runRootArgs(t, "plan", "list")
	if err != nil {
		t.Fatalf("plan list failed: %v", err)
	}
	if !contains(out, "PLAN-001") || !contains(out, "PLAN-002") {
		t.Errorf("expected both plans in list, got:\n%s", out)
	}

	// JSON output
	out, err = runRootArgs(t, "plan", "list", "--json")
	if err != nil {
		t.Fatalf("plan list --json failed: %v", err)
	}
	if !strings.Contains(out, `"id": "PLAN-001"`) {
		t.Errorf("expected JSON plan list, got:\n%s", out)
	}
}

func TestCmd_PlanSave_NotInitialized(t *testing.T) {
	dir, err := os.MkdirTemp("", "ctask-plan-noinit-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	chdir(t, dir)

	_, err = runRootArgs(t, "plan", "generate")
	if err == nil {
		t.Error("expected error when project not initialized")
	}
}

func TestCmd_PlanShow_NotExist(t *testing.T) {
	initTempProject(t)
	_, err := runRootArgs(t, "plan", "show", "PLAN-999")
	if err == nil {
		t.Error("expected error for missing plan")
	}
}
