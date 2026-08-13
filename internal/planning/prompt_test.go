package planning

import (
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
)

// sampleContext returns a small but representative PlanningContext.
func sampleContext() *model.PlanningContext {
	return &model.PlanningContext{
		ProjectName: "demo",
		GeneratedAt: "2026-08-09T10:00:00Z",
		Tasks: []model.TaskSnapshot{
			{ID: "TASK-001", Title: "Implement auth", Status: "done", Priority: "high"},
			{ID: "TASK-002", Title: "Add login tests", Status: "pending", Priority: "high", DependsOn: []string{"TASK-001"}},
			{ID: "TASK-003", Title: "Write docs", Status: "pending", Priority: "low"},
			{ID: "TASK-004", Title: "Wire API", Status: "blocked", Priority: "medium", DependsOn: []string{"TASK-002"}},
			{ID: "TASK-005", Title: "In-flight", Status: "in_progress", Priority: "medium", ClaimedBy: "codex"},
		},
		BlockedSummary: []model.BlockedTask{
			{ID: "TASK-004", Title: "Wire API", Reason: "waiting for spec"},
		},
		Decisions: []model.DecisionEntry{
			{Date: "2026-08-09", Decision: "Use YAML for storage", Context: "simplicity"},
		},
		EvidenceSummary: []model.EvidenceSnapshot{
			{TaskID: "TASK-001", Files: []string{"auth.go"}, TestResult: "passed", DiffStat: "1 file changed"},
		},
		CurrentAgent: "codex",
	}
}

func TestGeneratePrompt_PlanningMode(t *testing.T) {
	p := GeneratePrompt(sampleContext(), PromptModePlanning, "codex")
	for _, want := range []string{
		"# CodeLedger Project Context",
		"## Project",
		"demo",
		"## Task Status Overview",
		"| TASK-001 | done | high |",
		"## Ready to Start (no unmet dependencies)",
		"TASK-003", // low priority but no deps
		"## Blocked Tasks",
		"waiting for spec",
		"## Recent Decisions",
		"Use YAML for storage",
		"## Evidence Summary",
		"result=passed",
		"## Your Task",
		`You are agent "codex"`,
		"Recommendations:",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("planning prompt missing %q\n---\n%s", want, p)
		}
	}
}

func TestGeneratePrompt_TriageMode(t *testing.T) {
	p := GeneratePrompt(sampleContext(), PromptModeTriage, "codex")
	for _, want := range []string{
		"# CodeLedger Quick Triage",
		"## Ready Tasks (can start immediately)",
		"TASK-003",
		"## In Progress",
		"claimed by codex",
		"## Blocked",
		"waiting for spec",
		"start | reason",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("triage prompt missing %q\n---\n%s", want, p)
		}
	}
	if strings.Contains(p, "## Recent Decisions") {
		t.Error("triage prompt should not include decisions")
	}
}

func TestGeneratePrompt_BlockedMode(t *testing.T) {
	p := GeneratePrompt(sampleContext(), PromptModeBlocked, "")
	for _, want := range []string{
		"# CodeLedger Blocked Resolution",
		"## Blocked Tasks",
		"TASK-004",
		"waiting for spec",
		"unblock | action needed",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("blocked prompt missing %q\n---\n%s", want, p)
		}
	}
}

func TestGeneratePrompt_UnknownModeDefaultsToPlanning(t *testing.T) {
	p := GeneratePrompt(sampleContext(), "unknown-mode", "codex")
	if !strings.Contains(p, "# CodeLedger Project Context") {
		t.Error("unknown mode should fall back to planning prompt")
	}
}

func TestGeneratePrompt_NoReadyTasks(t *testing.T) {
	ctx := &model.PlanningContext{ProjectName: "empty", Tasks: []model.TaskSnapshot{
		{ID: "TASK-001", Title: "a", Status: "pending", DependsOn: []string{"TASK-002"}},
		{ID: "TASK-002", Title: "b", Status: "pending"},
	}}
	p := GeneratePrompt(ctx, PromptModePlanning, "codex")
	if !strings.Contains(p, "(none)") {
		t.Errorf("expected (none) for ready tasks, got:\n%s", p)
	}
}
