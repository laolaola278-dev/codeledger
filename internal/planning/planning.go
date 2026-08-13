package planning

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
)

// CollectPlanningContext gathers all relevant project state into a
// PlanningContext snapshot. It never reasons about the data: it only
// gathers and shapes it for the prompt engine.
func CollectPlanningContext(s *store.Store, agent string) (*model.PlanningContext, error) {
	p, err := s.ReadProject()
	if err != nil {
		return nil, fmt.Errorf("failed to read project: %w", err)
	}

	tl, err := s.ReadTasks()
	if err != nil {
		return nil, fmt.Errorf("failed to read tasks: %w", err)
	}

	ctx := &model.PlanningContext{
		ProjectName:  p.Name,
		GeneratedAt:  util.NowRFC3339(),
		CurrentAgent: agent,
		Tasks:        make([]model.TaskSnapshot, 0, len(tl.Tasks)),
	}

	// Agent claims: task_id -> agent from locks.yaml
	claims := map[string]string{}
	if locks, err := s.ReadLocks(); err == nil {
		for _, l := range locks.Locks {
			if !l.IsExpired() && l.Agent != "" {
				claims[l.TaskID] = l.Agent
			}
		}
	}

	for _, t := range tl.Tasks {
		snap := model.TaskSnapshot{
			ID:          t.ID,
			Title:       t.Title,
			Status:      t.Status,
			Priority:    t.Priority,
			DependsOn:   t.DependsOn,
			Files:       t.Files,
			Notes:       splitNotes(t.Notes),
			ClaimedBy:   claims[t.ID],
			LastUpdated: t.UpdatedAt,
		}
		ctx.Tasks = append(ctx.Tasks, snap)
	}

	// Blocked summary
	for _, t := range tl.Tasks {
		if t.Status == model.StatusBlocked {
			ctx.BlockedSummary = append(ctx.BlockedSummary, model.BlockedTask{
				ID:     t.ID,
				Title:  t.Title,
				Reason: t.BlockedReason,
			})
		}
	}

	// Decisions from decisions.md
	ctx.Decisions = parseDecisionsFile(s)

	// Evidence summary: last 5 completed tasks, with diff stat from disk
	recent, err := service.GetRecentDoneTasks(s, 5)
	if err == nil {
		for _, t := range recent {
			snap := model.EvidenceSnapshot{
				TaskID:      t.ID,
				Files:       t.Files,
				TestResult:  t.Test.Result,
				CompletedAt: t.CompletedAt,
			}
			if diffPath := s.EvidenceDiffPath(t.ID); fileExists(diffPath) {
				if data, err := os.ReadFile(diffPath); err == nil {
					snap.DiffStat = firstLines(string(data), 12)
				}
			}
			ctx.EvidenceSummary = append(ctx.EvidenceSummary, snap)
		}
	}

	return ctx, nil
}

// Generate collects the context and returns both the context snapshot and
// the generated prompt for the given mode and agent.
func Generate(s *store.Store, agent string, mode string) (*model.PlanningContext, string, error) {
	ctx, err := CollectPlanningContext(s, agent)
	if err != nil {
		return nil, "", err
	}
	prompt := GeneratePrompt(ctx, mode, agent)
	return ctx, prompt, nil
}

// splitNotes splits a task's newline-separated notes into lines.
func splitNotes(notes string) []string {
	if strings.TrimSpace(notes) == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(notes, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// parseDecisionsFile reads decisions.md and extracts dated decision entries
// (## YYYY-MM-DD blocks with - **Context:** / **Decision:** / **Consequences:**).
// Entries are returned in reversed chronological order (most recent first).
func parseDecisionsFile(s *store.Store) []model.DecisionEntry {
	data, err := os.ReadFile(s.DecisionsPath())
	if err != nil {
		return nil
	}

	var entries []model.DecisionEntry
	var current *model.DecisionEntry

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if current != nil {
				entries = append(entries, *current)
			}
			current = &model.DecisionEntry{Date: strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "- **Context:**"):
			current.Context = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **Context:**"))
		case strings.HasPrefix(trimmed, "- **Decision:**"):
			current.Decision = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **Decision:**"))
		case strings.HasPrefix(trimmed, "- **Consequences:**"):
			current.Consequences = strings.TrimSpace(strings.TrimPrefix(trimmed, "- **Consequences:**"))
		}
	}
	if current != nil {
		entries = append(entries, *current)
	}

	// Reversed chronological (most recent first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Date > entries[j].Date
	})
	return entries
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// normalizePlanID accepts "PLAN-001", "plan-001" or "PLAN-1" and returns
// the canonical "PLAN-001" form; empty is returned when input is invalid.
func normalizePlanID(id string) string {
	var num int
	upper := strings.ToUpper(strings.TrimSpace(id))
	if _, err := fmt.Sscanf(upper, "PLAN-%d", &num); err != nil {
		return ""
	}
	if num <= 0 {
		return ""
	}
	return fmt.Sprintf("PLAN-%03d", num)
}
