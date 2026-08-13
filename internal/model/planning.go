package model

// PlanningContext is a snapshot of the project state as seen by one agent.
// It is assembled from tasks.yaml / decisions.md / events.jsonl / evidence/
// and is the input for the prompt engine. CodeLedger only gathers data here;
// it never reasons about it.
type PlanningContext struct {
	ProjectName     string             `json:"project_name" yaml:"project_name"`
	GeneratedAt     string             `json:"generated_at" yaml:"generated_at"`
	Tasks           []TaskSnapshot     `json:"tasks" yaml:"tasks"`
	Decisions       []DecisionEntry    `json:"decisions" yaml:"decisions"`
	BlockedSummary  []BlockedTask      `json:"blocked_summary" yaml:"blocked_summary"`
	CurrentAgent    string             `json:"current_agent" yaml:"current_agent"`
	EvidenceSummary []EvidenceSnapshot `json:"evidence_summary" yaml:"evidence_summary"`
}

// TaskSnapshot is a lightweight, prompt-friendly view of a task.
type TaskSnapshot struct {
	ID          string   `json:"id" yaml:"id"`
	Title       string   `json:"title" yaml:"title"`
	Status      string   `json:"status" yaml:"status"`
	Priority    string   `json:"priority" yaml:"priority"`
	DependsOn   []string `json:"depends_on" yaml:"depends_on"`
	Files       []string `json:"files" yaml:"files"`
	Notes       []string `json:"notes" yaml:"notes"`
	ClaimedBy   string   `json:"claimed_by,omitempty" yaml:"claimed_by,omitempty"`
	LastUpdated string   `json:"last_updated" yaml:"last_updated"`
}

// DecisionEntry is a single parsed entry from decisions.md.
type DecisionEntry struct {
	Date         string `json:"date" yaml:"date"`
	Context      string `json:"context" yaml:"context"`
	Decision     string `json:"decision" yaml:"decision"`
	Consequences string `json:"consequences" yaml:"consequences"`
}

// BlockedTask summarizes a blocked task for the prompt.
type BlockedTask struct {
	ID     string `json:"id" yaml:"id"`
	Title  string `json:"title" yaml:"title"`
	Reason string `json:"reason" yaml:"reason"`
}

// EvidenceSnapshot summarizes the evidence of a completed task.
type EvidenceSnapshot struct {
	TaskID      string   `json:"task_id" yaml:"task_id"`
	Files       []string `json:"files" yaml:"files"`
	TestResult  string   `json:"test_result" yaml:"test_result"`
	DiffStat    string   `json:"diff_stat" yaml:"diff_stat"`
	CompletedAt string   `json:"completed_at" yaml:"completed_at"`
}

// PlanProposal is the persisted, auditable record of one planning run.
// The full prompt is stored so the reasoning can be audited later.
type PlanProposal struct {
	ID              string               `json:"id" yaml:"id"`
	GeneratedAt     string               `json:"generated_at" yaml:"generated_at"`
	GeneratedBy     string               `json:"generated_by" yaml:"generated_by"`
	Mode            string               `json:"mode,omitempty" yaml:"mode,omitempty"`
	Recommendations []TaskRecommendation `json:"recommendations" yaml:"recommendations"`
	Rationale       string               `json:"rationale" yaml:"rationale"`
	PromptUsed      string               `json:"prompt_used" yaml:"prompt_used"`
}

// TaskRecommendation is one suggested action from an agent's plan output.
type TaskRecommendation struct {
	TaskID       string   `json:"task_id" yaml:"task_id"`
	Action       string   `json:"action" yaml:"action"` // start / unblock / review / skip
	Priority     int      `json:"priority" yaml:"priority"`
	Reason       string   `json:"reason" yaml:"reason"`
	Dependencies []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
}

// IsValidPlanAction reports whether action is one of the supported actions.
func IsValidPlanAction(a string) bool {
	switch a {
	case "start", "unblock", "review", "skip":
		return true
	}
	return false
}
