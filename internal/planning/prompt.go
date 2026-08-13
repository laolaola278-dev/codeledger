package planning

import (
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/model"
)

// Three prompt modes, covering three usage scenarios.
const (
	PromptModePlanning = "planning" // long session: what should I do next?
	PromptModeTriage   = "triage"   // short session: what can I do right now?
	PromptModeBlocked  = "blocked"  // blocked: how do I unblock?
)

// GeneratePrompt assembles a PlanningContext into a structured, natural
// language prompt. It performs no LLM calls and no reasoning: it only
// formats the data that CodeLedger already gathered.
func GeneratePrompt(ctx *model.PlanningContext, mode string, agent string) string {
	switch mode {
	case PromptModeTriage:
		return buildTriagePrompt(ctx, agent)
	case PromptModeBlocked:
		return buildBlockedPrompt(ctx)
	default:
		return buildPlanningPrompt(ctx, agent)
	}
}

// buildPlanningPrompt is the full planning prompt: task graph, ready tasks,
// blocked tasks, recent decisions and evidence, then asks the agent to
// propose a plan.
func buildPlanningPrompt(ctx *model.PlanningContext, agent string) string {
	var b strings.Builder

	b.WriteString("# CodeLedger Project Context\n\n")

	b.WriteString("## Project\n")
	name := ctx.ProjectName
	if name == "" {
		name = "(unnamed)"
	}
	b.WriteString(fmt.Sprintf("%s — generated %s\n\n", name, ctx.GeneratedAt))

	b.WriteString("## Task Status Overview\n")
	b.WriteString("| Task | Status | Priority | Depends On | Claimed By |\n")
	b.WriteString("|------|--------|----------|------------|------------|\n")
	for _, t := range ctx.Tasks {
		deps := joinOrDash(t.DependsOn)
		claimed := t.ClaimedBy
		if claimed == "" {
			claimed = "-"
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n", t.ID, t.Status, t.Priority, deps, claimed))
	}
	b.WriteString("\n")

	// Ready to start: pending + all dependencies done
	doneSet := make(map[string]bool)
	for _, t := range ctx.Tasks {
		if t.Status == model.StatusDone {
			doneSet[t.ID] = true
		}
	}
	var ready []model.TaskSnapshot
	for _, t := range ctx.Tasks {
		if t.Status != model.StatusPending {
			continue
		}
		allDepsDone := true
		for _, d := range t.DependsOn {
			if !doneSet[d] {
				allDepsDone = false
				break
			}
		}
		if allDepsDone {
			ready = append(ready, t)
		}
	}
	b.WriteString("## Ready to Start (no unmet dependencies)\n")
	if len(ready) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, t := range ready {
			b.WriteString(fmt.Sprintf("- %s: %s (%s)\n", t.ID, t.Title, t.Priority))
		}
	}
	b.WriteString("\n")

	// Blocked tasks
	b.WriteString("## Blocked Tasks\n")
	if len(ctx.BlockedSummary) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, bt := range ctx.BlockedSummary {
			reason := bt.Reason
			if reason == "" {
				reason = "(no reason given)"
			}
			b.WriteString(fmt.Sprintf("- %s: %s — %s\n", bt.ID, bt.Title, reason))
		}
	}
	b.WriteString("\n")

	// Recent decisions (reversed chronological)
	b.WriteString("## Recent Decisions\n")
	if len(ctx.Decisions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, d := range ctx.Decisions {
			b.WriteString(fmt.Sprintf("- [%s] %s\n", d.Date, d.Decision))
			if d.Context != "" {
				b.WriteString(fmt.Sprintf("  Context: %s\n", d.Context))
			}
		}
	}
	b.WriteString("\n")

	// Evidence summary (last 5 completed tasks)
	b.WriteString("## Evidence Summary (last 5 completed tasks)\n")
	if len(ctx.EvidenceSummary) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, e := range ctx.EvidenceSummary {
			result := e.TestResult
			if result == "" {
				result = "unknown"
			}
			b.WriteString(fmt.Sprintf("- %s: result=%s files=%s diff_stat=%q\n",
				e.TaskID, result, joinOrDash(e.Files), strings.TrimSpace(e.DiffStat)))
		}
	}
	b.WriteString("\n")

	// Agent task
	b.WriteString("## Your Task\n")
	b.WriteString(fmt.Sprintf("You are agent %q working on this project. Based on the task graph above, propose a plan for what to work on next. Consider:\n\n", agent))
	b.WriteString("1. Which tasks are unblocked and ready?\n")
	b.WriteString("2. Which tasks are high priority?\n")
	b.WriteString("3. Are there tasks that should be split or skipped?\n")
	b.WriteString("4. If you're already claiming a task, is it making progress?\n\n")
	b.WriteString("Output format:\n")
	b.WriteString("```\n")
	b.WriteString("PLAN-XXX\n")
	b.WriteString("Recommendations:\n")
	b.WriteString("- TASK-XXX: start | reason\n")
	b.WriteString("- TASK-YYY: unblock | action needed\n")
	b.WriteString("- TASK-ZZZ: skip | reason\n")
	b.WriteString("\n")
	b.WriteString("Rationale: <why this order>\n")
	b.WriteString("```\n")

	return b.String()
}

// buildTriagePrompt is a compact prompt for short sessions: it focuses on
// what can be done immediately (ready tasks + quick wins) and skips the
// full decision history.
func buildTriagePrompt(ctx *model.PlanningContext, agent string) string {
	var b strings.Builder

	b.WriteString("# CodeLedger Quick Triage\n\n")
	b.WriteString(fmt.Sprintf("Project: %s — generated %s\n\n", ctx.ProjectName, ctx.GeneratedAt))

	b.WriteString("## Ready Tasks (can start immediately)\n")
	doneSet := make(map[string]bool)
	for _, t := range ctx.Tasks {
		if t.Status == model.StatusDone {
			doneSet[t.ID] = true
		}
	}
	var ready []model.TaskSnapshot
	for _, t := range ctx.Tasks {
		if t.Status != model.StatusPending {
			continue
		}
		allDepsDone := true
		for _, d := range t.DependsOn {
			if !doneSet[d] {
				allDepsDone = false
				break
			}
		}
		if allDepsDone {
			ready = append(ready, t)
		}
	}
	if len(ready) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, t := range ready {
			b.WriteString(fmt.Sprintf("- %s: %s (priority=%s)\n", t.ID, t.Title, t.Priority))
		}
	}
	b.WriteString("\n")

	b.WriteString("## In Progress\n")
	var inProgress []model.TaskSnapshot
	for _, t := range ctx.Tasks {
		if t.Status == model.StatusInProgress {
			inProgress = append(inProgress, t)
		}
	}
	if len(inProgress) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, t := range inProgress {
			by := t.ClaimedBy
			if by == "" {
				by = "unknown agent"
			}
			b.WriteString(fmt.Sprintf("- %s: %s (claimed by %s)\n", t.ID, t.Title, by))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Blocked\n")
	if len(ctx.BlockedSummary) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, bt := range ctx.BlockedSummary {
			b.WriteString(fmt.Sprintf("- %s: %s\n", bt.ID, bt.Title))
			if bt.Reason != "" {
				b.WriteString(fmt.Sprintf("  Reason: %s\n", bt.Reason))
			}
		}
	}
	b.WriteString("\n")

	b.WriteString("## Your Task\n")
	b.WriteString(fmt.Sprintf("You are agent %q. This is a short session: pick at most one task you can start right now and state it as:\n\n", agent))
	b.WriteString("- TASK-XXX: start | reason\n")
	b.WriteString("If nothing is ready, say what is blocking you.\n")

	return b.String()
}

// buildBlockedPrompt focuses on unblocking: current blockers + relevant
// context so the agent can propose unblock actions.
func buildBlockedPrompt(ctx *model.PlanningContext) string {
	var b strings.Builder

	b.WriteString("# CodeLedger Blocked Resolution\n\n")
	b.WriteString(fmt.Sprintf("Project: %s — generated %s\n\n", ctx.ProjectName, ctx.GeneratedAt))

	b.WriteString("## Blocked Tasks\n")
	if len(ctx.BlockedSummary) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, bt := range ctx.BlockedSummary {
			b.WriteString(fmt.Sprintf("- %s: %s\n", bt.ID, bt.Title))
			if bt.Reason != "" {
				b.WriteString(fmt.Sprintf("  Reason: %s\n", bt.Reason))
			}
		}
	}
	b.WriteString("\n")

	b.WriteString("## Ready Tasks\n")
	doneSet := make(map[string]bool)
	for _, t := range ctx.Tasks {
		if t.Status == model.StatusDone {
			doneSet[t.ID] = true
		}
	}
	for _, t := range ctx.Tasks {
		if t.Status != model.StatusPending {
			continue
		}
		allDepsDone := true
		for _, d := range t.DependsOn {
			if !doneSet[d] {
				allDepsDone = false
				break
			}
		}
		if allDepsDone {
			b.WriteString(fmt.Sprintf("- %s: %s\n", t.ID, t.Title))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Recent Decisions\n")
	if len(ctx.Decisions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, d := range ctx.Decisions {
			b.WriteString(fmt.Sprintf("- [%s] %s\n", d.Date, d.Decision))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Your Task\n")
	b.WriteString("For each blocked task above, propose an unblock action. Output format:\n")
	b.WriteString("```\n")
	b.WriteString("Recommendations:\n")
	b.WriteString("- TASK-XXX: unblock | action needed\n")
	b.WriteString("- TASK-YYY: skip | reason\n")
	b.WriteString("\n")
	b.WriteString("Rationale: <why this order>\n")
	b.WriteString("```\n")

	return b.String()
}

// joinOrDash joins a slice with ", ", returning "-" when empty.
func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}
