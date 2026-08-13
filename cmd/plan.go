package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/planning"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

var (
	planMode       string
	planAgent      string
	planInput      string
	planFile       string
	planJSON       bool
	planList       bool
	planBy         string
	planPrompt     bool
	planSavePrompt string
	planPromptFile string
	planSaveMode   string
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "AI-assisted planning: generate prompts, save and show plans",
	Long: `AI-assisted planning for CodeLedger.

CodeLedger does not reason: it only assembles the current .ctask/ state into a
structured prompt that you hand to your own AI agent. The agent's model does
the reasoning, and the result can be saved back as an auditable plan.

Subcommands:
  generate    Print a structured planning prompt to stdout
  save        Parse an agent's plan text and persist it to .ctask/plans/
  show        Show a saved plan (PLAN-001)
  list        List all saved plans`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var planGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Print a structured planning prompt to stdout",
	Long: `Generate a structured prompt from the current .ctask/ state and print it
to stdout. The prompt is plain text: CodeLedger performs no LLM calls.

The agent (or human) copies the prompt, runs it through its own model, then
saves the result with 'ctask plan save'.

Flags:
  --mode    planning | triage | blocked  (default: planning)
  --agent   Agent name to embed in the prompt (e.g. codex)
  --json    Also print the machine-readable PlanningContext as JSON after the prompt`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		mode := planMode
		if mode == "" {
			mode = planning.PromptModePlanning
		}
		if mode != planning.PromptModePlanning && mode != planning.PromptModeTriage && mode != planning.PromptModeBlocked {
			return fmt.Errorf("invalid mode %q (use planning, triage or blocked)", mode)
		}

		ctx, prompt, err := planning.Generate(s, planAgent, mode)
		if err != nil {
			return fmt.Errorf("plan generate failed: %w", err)
		}

		fmt.Println(prompt)

		if planJSON {
			// machine-readable context snapshot follows the prompt
			data, err := json.MarshalIndent(ctx, "", "  ")
			if err != nil {
				return fmt.Errorf("plan generate: failed to marshal context: %w", err)
			}
			fmt.Println("\n--- context ---")
			fmt.Println(string(data))
		}
		return nil
	},
}

var planSaveCmd = &cobra.Command{
	Use:   "save [PLAN-XXX]",
	Short: "Parse an agent's plan text and persist it to .ctask/plans/",
	Long: `Parse a plan returned by an agent and save it to .ctask/plans/PLAN-XXX.yaml.

Provide the plan text via --input, or via a file with --file. If no plan ID is
given, the next free PLAN-XXX is used. A plan.saved event is appended to
events.jsonl.

Example:
  ctask plan save --input "TASK-003: start | highest priority unblocked task"
  ctask plan save PLAN-002 --file plan.txt

Flags:
  --input         Plan text (recommendations + rationale)
  --file          Read plan text from a file
  --prompt        The full prompt text that was used to generate this plan
  --prompt-file   Read the prompt text from a file
  --mode          Record the plan mode: planning | triage | blocked
  --agent         Record which agent generated this plan`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		if planInput == "" && planFile == "" {
			return fmt.Errorf("provide plan text via --input or --file")
		}
		if planInput != "" && planFile != "" {
			return fmt.Errorf("use either --input or --file, not both")
		}

		text := planInput
		if planFile != "" {
			data, err := os.ReadFile(planFile)
			if err != nil {
				return fmt.Errorf("failed to read plan file: %w", err)
			}
			text = string(data)
		}

		proposal, err := planning.ParsePlanOutput(text)
		if err != nil {
			return fmt.Errorf("plan save: %w", err)
		}

		if len(args) == 1 {
			proposal.ID = args[0]
		}
		proposal.GeneratedBy = planBy
		if proposal.GeneratedBy == "" {
			proposal.GeneratedBy = planAgent
		}

		// Record the prompt used to generate this plan (audit channel).
		switch {
		case planSavePrompt != "" && planPromptFile != "":
			return fmt.Errorf("use either --prompt or --prompt-file, not both")
		case planPromptFile != "":
			data, err := os.ReadFile(planPromptFile)
			if err != nil {
				return fmt.Errorf("failed to read prompt file: %w", err)
			}
			proposal.PromptUsed = string(data)
		case planSavePrompt != "":
			proposal.PromptUsed = planSavePrompt
		}

		// Record the plan mode (planning / triage / blocked). Invalid values
		// are ignored, matching the lenient ParsePlanOutput philosophy.
		if planSaveMode != "" && isValidPlanMode(planSaveMode) {
			proposal.Mode = planSaveMode
		}

		if err := withProjectLock(s, "plan save", planAgent, "", func() error {
			return planning.SavePlan(s, proposal)
		}); err != nil {
			return fmt.Errorf("plan save failed: %w", err)
		}

		fmt.Printf("Plan %s saved to %s\n", proposal.ID, s.PlanPath(proposal.ID))
		return nil
	},
}

var planShowCmd = &cobra.Command{
	Use:   "show <plan-id>",
	Short: "Show a saved plan",
	Long: `Display a previously saved plan from .ctask/plans/<plan-id>.yaml.

Use 'ctask plan list' to see all saved plans.

Flags:
  --prompt    Also print the full prompt that was used to generate this plan`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		id := args[0]
		plan, err := s.ReadPlan(id)
		if err != nil {
			return err
		}

		printPlan(plan)
		if planPrompt {
			if plan.PromptUsed != "" {
				fmt.Println("\n--- Prompt Used ---")
				fmt.Println(plan.PromptUsed)
			} else {
				fmt.Println("\n(no prompt recorded for this plan)")
			}
		}
		return nil
	},
}

var planListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all saved plans",
	Long: `List all plans saved in .ctask/plans/, newest first.

Flags:
  --json    Output as JSON`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := store.NewStore(".")
		if err := s.RequireInit(); err != nil {
			return err
		}

		plans, err := s.ListPlans()
		if err != nil {
			return fmt.Errorf("plan list failed: %w", err)
		}

		if planJSON {
			out, err := json.MarshalIndent(plans, "", "  ")
			if err != nil {
				return fmt.Errorf("plan list: failed to marshal JSON: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		if len(plans) == 0 {
			fmt.Println("No plans saved yet. Use 'ctask plan save' to record one.")
			return nil
		}

		// newest first
		for i := len(plans) - 1; i >= 0; i-- {
			p := plans[i]
			by := p.GeneratedBy
			if by == "" {
				by = "unknown"
			}
			fmt.Printf("%s  %s  by %s  (%d recommendation(s))\n", p.ID, p.GeneratedAt, by, len(p.Recommendations))
		}
		return nil
	},
}

// isValidPlanMode reports whether mode is one of the supported plan modes.
// Unknown modes are ignored by plan save (lenient, like ParsePlanOutput).
func isValidPlanMode(mode string) bool {
	switch mode {
	case planning.PromptModePlanning, planning.PromptModeTriage, planning.PromptModeBlocked:
		return true
	}
	return false
}

// printPlan renders a plan proposal in a human-friendly way.
func printPlan(p *model.PlanProposal) {
	fmt.Printf("%s (generated %s", p.ID, p.GeneratedAt)
	if p.GeneratedBy != "" {
		fmt.Printf(" by %s", p.GeneratedBy)
	}
	fmt.Println(")")
	if len(p.Recommendations) == 0 {
		fmt.Println("  No recommendations.")
	}
	for _, r := range p.Recommendations {
		reason := r.Reason
		if reason == "" {
			reason = "(no reason)"
		}
		fmt.Printf("  %s: %s | %s\n", r.TaskID, r.Action, reason)
	}
	if p.Rationale != "" {
		fmt.Printf("\nRationale: %s\n", p.Rationale)
	}
}

func init() {
	planGenerateCmd.Flags().StringVar(&planMode, "mode", planning.PromptModePlanning, "Prompt mode: planning, triage, blocked")
	planGenerateCmd.Flags().StringVar(&planAgent, "agent", "", "Agent name to embed in the prompt")
	planGenerateCmd.Flags().BoolVar(&planJSON, "json", false, "Also print the machine-readable PlanningContext as JSON")

	planSaveCmd.Flags().StringVar(&planInput, "input", "", "Plan text (recommendations + rationale)")
	planSaveCmd.Flags().StringVar(&planFile, "file", "", "Read plan text from a file")
	planSaveCmd.Flags().StringVar(&planSavePrompt, "prompt", "", "Full prompt text used to generate this plan")
	planSaveCmd.Flags().StringVar(&planPromptFile, "prompt-file", "", "Read the prompt text from a file")
	planSaveCmd.Flags().StringVar(&planSaveMode, "mode", "", "Record the plan mode: planning, triage, blocked")
	planSaveCmd.Flags().StringVar(&planAgent, "agent", "", "Agent name recorded as plan author")
	planSaveCmd.Flags().StringVar(&planBy, "by", "", "Alias for --agent on save")

	planShowCmd.Flags().BoolVar(&planPrompt, "prompt", false, "Also print the full prompt used for this plan")

	planListCmd.Flags().BoolVar(&planJSON, "json", false, "Output as JSON")

	planCmd.AddCommand(planGenerateCmd)
	planCmd.AddCommand(planSaveCmd)
	planCmd.AddCommand(planShowCmd)
	planCmd.AddCommand(planListCmd)
	rootCmd.AddCommand(planCmd)
}
