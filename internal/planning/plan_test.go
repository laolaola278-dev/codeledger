package planning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
)

func TestParsePlanOutput_StandardFormat(t *testing.T) {
	out := `PLAN-001
Recommendations:
- TASK-003: start | highest priority unblocked task
- TASK-004: unblock | need API spec
- TASK-002: skip | blocked on design

Rationale: start with the highest priority work first.`
	p, err := ParsePlanOutput(out)
	if err != nil {
		t.Fatalf("ParsePlanOutput failed: %v", err)
	}
	if p.ID != "PLAN-001" {
		t.Errorf("expected PLAN-001, got %s", p.ID)
	}
	if len(p.Recommendations) != 3 {
		t.Fatalf("expected 3 recommendations, got %d", len(p.Recommendations))
	}
	if p.Recommendations[0].TaskID != "TASK-003" || p.Recommendations[0].Action != "start" {
		t.Errorf("bad first rec: %+v", p.Recommendations[0])
	}
	if p.Recommendations[1].Action != "unblock" {
		t.Errorf("bad second action: %s", p.Recommendations[1].Action)
	}
	if p.Recommendations[2].Action != "skip" {
		t.Errorf("bad third action: %s", p.Recommendations[2].Action)
	}
	if !strings.Contains(p.Rationale, "start with the highest priority") {
		t.Errorf("rationale not captured: %q", p.Rationale)
	}
}

func TestParsePlanOutput_FlexibleFormat(t *testing.T) {
	out := `* TASK-002: unblock | need API spec (depends: TASK-001)
1. TASK-005: review | sanity check
TASK-003: start | quick win`
	p, err := ParsePlanOutput(out)
	if err != nil {
		t.Fatalf("ParsePlanOutput failed: %v", err)
	}
	if p.ID != "" {
		t.Errorf("missing header should leave ID empty for auto-assign, got %s", p.ID)
	}
	if len(p.Recommendations) != 3 {
		t.Fatalf("expected 3 recommendations, got %d", len(p.Recommendations))
	}
	if p.Recommendations[0].TaskID != "TASK-002" || p.Recommendations[0].Action != "unblock" {
		t.Errorf("bad rec: %+v", p.Recommendations[0])
	}
	if p.Recommendations[2].Action != "start" {
		t.Errorf("plain 'TASK: action' line not parsed: %+v", p.Recommendations[2])
	}
}

func TestParsePlanOutput_UnknownActionDefaultsToReview(t *testing.T) {
	p, err := ParsePlanOutput("TASK-001: explode | boom")
	if err != nil {
		t.Fatalf("ParsePlanOutput failed: %v", err)
	}
	if p.Recommendations[0].Action != "review" {
		t.Errorf("unknown action should default to review, got %s", p.Recommendations[0].Action)
	}
}

func TestParsePlanOutput_Empty(t *testing.T) {
	if _, err := ParsePlanOutput("   \n  "); err == nil {
		t.Error("expected error for empty output")
	}
}

func TestSavePlan_RoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "ctask-plan-store-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	s := store.NewStore(dir)
	if err := service.InitProject(s); err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}

	p := &model.PlanProposal{
		GeneratedBy: "codex",
		Recommendations: []model.TaskRecommendation{
			{TaskID: "TASK-001", Action: "start", Reason: "quick win"},
		},
		Rationale: "do the quick win first",
	}
	if err := SavePlan(s, p); err != nil {
		t.Fatalf("SavePlan failed: %v", err)
	}
	if p.ID != "PLAN-001" {
		t.Errorf("expected auto-assigned PLAN-001, got %s", p.ID)
	}

	got, err := s.ReadPlan("PLAN-001")
	if err != nil {
		t.Fatalf("ReadPlan failed: %v", err)
	}
	if got.ID != "PLAN-001" || got.GeneratedBy != "codex" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Recommendations) != 1 || got.Recommendations[0].TaskID != "TASK-001" {
		t.Errorf("bad recommendations: %+v", got.Recommendations)
	}
	if !strings.Contains(got.Rationale, "quick win") {
		t.Errorf("rationale lost: %q", got.Rationale)
	}

	// plan.saved event must be recorded
	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
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

	// file on disk
	if _, err := os.Stat(filepath.Join(s.PlanDirPath(), "PLAN-001.yaml")); err != nil {
		t.Errorf("plan file not on disk: %v", err)
	}
}

func TestStore_NextPlanIDAndList(t *testing.T) {
	dir, err := os.MkdirTemp("", "ctask-plan-list-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	s := store.NewStore(dir)
	if err := service.InitProject(s); err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}

	id1, err := s.NextPlanID()
	if err != nil || id1 != "PLAN-001" {
		t.Fatalf("first NextPlanID = %s, %v", id1, err)
	}
	if err := s.SavePlan(&model.PlanProposal{GeneratedBy: "a"}); err != nil {
		t.Fatalf("save 1 failed: %v", err)
	}
	if err := s.SavePlan(&model.PlanProposal{GeneratedBy: "b"}); err != nil {
		t.Fatalf("save 2 failed: %v", err)
	}
	id3, err := s.NextPlanID()
	if err != nil || id3 != "PLAN-003" {
		t.Fatalf("third NextPlanID = %s, %v", id3, err)
	}

	plans, err := s.ListPlans()
	if err != nil {
		t.Fatalf("ListPlans failed: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].ID != "PLAN-001" || plans[1].ID != "PLAN-002" {
		t.Errorf("plans not sorted: %s, %s", plans[0].ID, plans[1].ID)
	}
}

func TestNormalizePlanID(t *testing.T) {
	cases := map[string]string{
		"PLAN-001": "PLAN-001",
		"plan-1":   "PLAN-001",
		"PLAN-42":  "PLAN-042",
		"":         "",
		"TASK-001": "",
		"PLAN-0":   "",
		"abc":      "",
	}
	for in, want := range cases {
		if got := normalizePlanID(in); got != want {
			t.Errorf("normalizePlanID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRecommendation(t *testing.T) {
	rec, ok := ParseRecommendation("TASK-009: unblock | need creds")
	if !ok {
		t.Fatal("expected parse success")
	}
	if rec.TaskID != "TASK-009" || rec.Action != "unblock" || rec.Reason != "need creds" {
		t.Errorf("bad rec: %+v", rec)
	}
	if _, ok := ParseRecommendation("not a rec"); ok {
		t.Error("expected parse failure")
	}
}
