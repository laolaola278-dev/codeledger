package planning

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
)

// planHeaderRe matches the PLAN-001 / plan-001 header line.
var planHeaderRe = regexp.MustCompile(`(?i)^\s*(PLAN-\d+)\s*$`)

// recommendationRe matches a recommendation bullet.
// Examples accepted:
//
//   - TASK-003: start | highest priority unblocked task
//   - TASK-002: unblock | need API spec (depends: TASK-001)
//   - TASK-001: review | sanity check
//   - TASK-003: start | quick win (no bullet)
var recommendationRe = regexp.MustCompile(`(?i)^\s*(?:[-*]|\d+[.)])?\s*(TASK-\d+)\s*:\s*([a-zA-Z]+)\s*(?:\|\s*(.+?))?\s*$`)

// ParsePlanOutput parses a free-form plan text returned by an agent into a
// structured PlanProposal. It is lenient on purpose: unknown actions, missing
// recommendations and a missing header all fall back to defaults instead of
// failing, so agent output that does not match the template exactly is still
// recorded (and remains auditable via PromptUsed / Rationale).
func ParsePlanOutput(output string) (*model.PlanProposal, error) {
	if strings.TrimSpace(output) == "" {
		return nil, fmt.Errorf("plan output is empty")
	}

	proposal := &model.PlanProposal{}
	var rationaleParts []string

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if m := planHeaderRe.FindStringSubmatch(trimmed); m != nil {
			proposal.ID = normalizePlanID(m[1])
			continue
		}

		if m := recommendationRe.FindStringSubmatch(trimmed); m != nil {
			rec := model.TaskRecommendation{
				TaskID: strings.ToUpper(m[1]),
				Action: strings.ToLower(m[2]),
				Reason: strings.TrimSpace(m[3]),
			}
			if !model.IsValidPlanAction(rec.Action) {
				rec.Action = "review"
			}
			proposal.Recommendations = append(proposal.Recommendations, rec)
			continue
		}

		// Anything else is treated as rationale (including "Rationale: ..." lines).
		text := strings.TrimPrefix(trimmed, "Rationale:")
		text = strings.TrimPrefix(text, "rationale:")
		text = strings.TrimSpace(text)
		if text != "" {
			rationaleParts = append(rationaleParts, text)
		}
	}

	// No PLAN- header in the output: leave proposal.ID empty so the store can
	// auto-assign the next free PLAN-XXX on save.
	if len(rationaleParts) > 0 {
		proposal.Rationale = strings.Join(rationaleParts, " ")
	}
	return proposal, nil
}

// SavePlan persists a parsed plan proposal to .ctask/plans/ and logs a
// plan.saved event. The proposal's timestamps are filled in when missing.
func SavePlan(s *store.Store, proposal *model.PlanProposal) error {
	if proposal == nil {
		return fmt.Errorf("cannot save nil plan")
	}
	if proposal.GeneratedAt == "" {
		proposal.GeneratedAt = util.NowRFC3339()
	}
	if proposal.ID != "" {
		proposal.ID = normalizePlanID(proposal.ID)
	}
	if err := s.SavePlan(proposal); err != nil {
		return err
	}
	// The second argument (taskID) is intentionally empty: plan events have no
	// associated TASK-XXX. The plan ID doubles as the agent identity marker,
	// so it is passed as the agent (title) argument.
	evt := model.NewEvent(model.EventPlanSaved, "", proposal.ID, planEventMessage(proposal))
	return s.AppendEvent(evt)
}

// planEventMessage builds a compact event message for a saved plan.
func planEventMessage(p *model.PlanProposal) string {
	var b strings.Builder
	b.WriteString("plan saved: ")
	if p.GeneratedBy != "" {
		b.WriteString("by " + p.GeneratedBy + " ")
	}
	b.WriteString(fmt.Sprintf("%d recommendation(s)", len(p.Recommendations)))
	if len(p.Recommendations) > 0 {
		var ids []string
		for _, r := range p.Recommendations {
			ids = append(ids, r.TaskID+":"+r.Action)
		}
		b.WriteString(" [" + strings.Join(ids, ", ") + "]")
	}
	return b.String()
}

// ParseRecommendation parses a single recommendation string of the form
// "TASK-XXX: action | reason". Used by tests and by CLI --input parsing.
func ParseRecommendation(s string) (model.TaskRecommendation, bool) {
	m := recommendationRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return model.TaskRecommendation{}, false
	}
	rec := model.TaskRecommendation{
		TaskID: strings.ToUpper(m[1]),
		Action: strings.ToLower(m[2]),
		Reason: strings.TrimSpace(m[3]),
	}
	if !model.IsValidPlanAction(rec.Action) {
		rec.Action = "review"
	}
	return rec, true
}
