package planning

import (
	"os"
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
)

// newTestStore initializes a temp .ctask project and returns the store.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir, err := os.MkdirTemp("", "ctask-planning-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s := store.NewStore(dir)
	if err := service.InitProject(s); err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}
	return s
}

func TestCollectPlanningContext_TaskAndBlocked(t *testing.T) {
	s := newTestStore(t)

	tl, _ := s.ReadTasks()
	tl.Tasks = []model.Task{
		{ID: "TASK-001", Title: "auth", Status: model.StatusDone, Priority: model.PriorityHigh, Files: []string{"auth.go"}, Test: model.TaskTest{Result: "passed"}, CompletedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z"},
		{ID: "TASK-002", Title: "tests", Status: model.StatusPending, Priority: model.PriorityHigh, DependsOn: []string{"TASK-001"}, Notes: "line1\nline2", UpdatedAt: "2026-08-09T01:00:00Z"},
		{ID: "TASK-003", Title: "api", Status: model.StatusBlocked, Priority: model.PriorityMedium, BlockedReason: "waiting for spec", UpdatedAt: "2026-08-09T02:00:00Z"},
	}
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}

	ctx, err := CollectPlanningContext(s, "codex")
	if err != nil {
		t.Fatalf("CollectPlanningContext failed: %v", err)
	}

	if ctx.ProjectName == "" || ctx.CurrentAgent != "codex" {
		t.Errorf("bad context header: %+v", ctx)
	}
	if len(ctx.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(ctx.Tasks))
	}

	// Notes split into lines
	var snap *model.TaskSnapshot
	for i := range ctx.Tasks {
		if ctx.Tasks[i].ID == "TASK-002" {
			snap = &ctx.Tasks[i]
		}
	}
	if snap == nil {
		t.Fatal("TASK-002 snapshot not found")
	}
	if len(snap.Notes) != 2 || snap.Notes[0] != "line1" {
		t.Errorf("notes not split: %+v", snap.Notes)
	}

	// Blocked summary
	if len(ctx.BlockedSummary) != 1 || ctx.BlockedSummary[0].Reason != "waiting for spec" {
		t.Errorf("bad blocked summary: %+v", ctx.BlockedSummary)
	}
}

func TestCollectPlanningContext_DecisionsParsed(t *testing.T) {
	s := newTestStore(t)

	// decisions.md with two entries
	content := `# Decisions

## 2026-08-08

- **Context:** older context
- **Decision:** older decision
- **Consequences:** older consequences

## 2026-08-09

- **Context:** newer context
- **Decision:** newer decision
- **Consequences:** newer consequences
`
	if err := os.WriteFile(s.DecisionsPath(), []byte(content), 0644); err != nil {
		t.Fatalf("write decisions failed: %v", err)
	}

	ctx, err := CollectPlanningContext(s, "")
	if err != nil {
		t.Fatalf("CollectPlanningContext failed: %v", err)
	}
	if len(ctx.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(ctx.Decisions))
	}
	// reversed chronological
	if ctx.Decisions[0].Date != "2026-08-09" || ctx.Decisions[1].Date != "2026-08-08" {
		t.Errorf("decisions not reversed: %+v", ctx.Decisions)
	}
	if ctx.Decisions[0].Decision != "newer decision" {
		t.Errorf("bad decision text: %+v", ctx.Decisions[0])
	}
}

func TestCollectPlanningContext_EvidenceSummary(t *testing.T) {
	s := newTestStore(t)

	tl, _ := s.ReadTasks()
	tl.Tasks = []model.Task{
		{ID: "TASK-001", Title: "auth", Status: model.StatusDone, Priority: model.PriorityHigh, Files: []string{"auth.go"}, Test: model.TaskTest{Command: "go test ./...", Result: "passed"}, CompletedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z", Evidence: []string{"evidence/TASK-001.md"}},
	}
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}

	// write a .diff evidence file so the diff stat can be picked up
	if err := s.EnsureEvidenceDir(); err != nil {
		t.Fatalf("EnsureEvidenceDir failed: %v", err)
	}
	diffContent := "diff --git a/auth.go b/auth.go\nindex 000..111 100644\n--- a/auth.go\n+++ b/auth.go\n@@ -1 +1 @@\n+package auth\n"
	if err := os.WriteFile(s.EvidenceDiffPath("TASK-001"), []byte(diffContent), 0644); err != nil {
		t.Fatalf("write diff evidence failed: %v", err)
	}

	ctx, err := CollectPlanningContext(s, "")
	if err != nil {
		t.Fatalf("CollectPlanningContext failed: %v", err)
	}
	if len(ctx.EvidenceSummary) != 1 {
		t.Fatalf("expected 1 evidence summary, got %d", len(ctx.EvidenceSummary))
	}
	e := ctx.EvidenceSummary[0]
	if e.TaskID != "TASK-001" || e.TestResult != "passed" {
		t.Errorf("bad evidence summary: %+v", e)
	}
	if !strings.Contains(e.DiffStat, "diff --git a/auth.go") {
		t.Errorf("diff stat not read from .diff file: %q", e.DiffStat)
	}
}

func TestCollectPlanningContext_EmptyProject(t *testing.T) {
	s := newTestStore(t)
	ctx, err := CollectPlanningContext(s, "codex")
	if err != nil {
		t.Fatalf("CollectPlanningContext failed on empty project: %v", err)
	}
	if len(ctx.Tasks) != 0 || len(ctx.Decisions) != 0 || len(ctx.BlockedSummary) != 0 {
		t.Errorf("empty project should have no data: %+v", ctx)
	}
}

func TestGenerate_ReturnsPrompt(t *testing.T) {
	s := newTestStore(t)

	tl, _ := s.ReadTasks()
	tl.Tasks = []model.Task{
		{ID: "TASK-001", Title: "auth", Status: model.StatusDone, Priority: model.PriorityHigh, UpdatedAt: "2026-08-09T00:00:00Z"},
		{ID: "TASK-002", Title: "tests", Status: model.StatusPending, Priority: model.PriorityHigh, DependsOn: []string{"TASK-001"}, UpdatedAt: "2026-08-09T01:00:00Z"},
	}
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}

	ctx, prompt, err := Generate(s, "codex", PromptModePlanning)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if ctx == nil || prompt == "" {
		t.Fatal("expected non-empty context and prompt")
	}
	if !strings.Contains(prompt, "TASK-002") {
		t.Errorf("prompt should mention TASK-002:\n%s", prompt)
	}
}
