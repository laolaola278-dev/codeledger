package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/planning"
	"github.com/spf13/cobra"
)

type planGenerateOptions struct {
	mode  string
	agent string
	json  bool
}

type planSaveOptions struct {
	input      string
	file       string
	prompt     string
	promptFile string
	mode       string
	agent      string
	by         string
}

type planShowOptions struct {
	prompt bool
}

type planListOptions struct {
	json bool
}

func newPlanCmd(deps Dependencies) *cobra.Command {
	cmd := newCommand("plan", "AI-assisted planning: generate prompts, save and show plans",
		`AI-assisted planning for CodeLedger.

CodeLedger does not reason: it only assembles the current .ctask/ state into a
structured prompt that you hand to your own AI agent. The agent's model does
the reasoning, and the result can be saved back as an auditable plan.

Subcommands:
  generate    Print a structured planning prompt to stdout
  save        Parse an agent's plan text and persist it to .ctask/plans/
  show        Show a saved plan (PLAN-001)
  list        List all saved plans`)
	cmd.Args = noArgs()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	}

	cmd.AddCommand(
		newPlanGenerateCmd(deps),
		newPlanSaveCmd(deps),
		newPlanShowCmd(deps),
		newPlanListCmd(deps),
	)
	return cmd
}

func newPlanGenerateCmd(deps Dependencies) *cobra.Command {
	o := &planGenerateOptions{}
	cmd := newCommand("generate", "Print a structured planning prompt to stdout",
		`Generate a structured prompt from the current .ctask/ state and print it
to stdout. The prompt is plain text: CodeLedger performs no LLM calls.

The agent (or human) copies the prompt, runs it through its own model, then
saves the result with 'ctask plan save'.

Flags:
  --mode    planning | triage | blocked  (default: planning)
  --agent   Agent name to embed in the prompt (e.g. codex)
  --json    Also print the machine-readable PlanningContext as JSON after the prompt`)
	cmd.Args = noArgs()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		mode := o.mode
		if mode == "" {
			mode = planning.PromptModePlanning
		}
		if mode != planning.PromptModePlanning && mode != planning.PromptModeTriage && mode != planning.PromptModeBlocked {
			return clierr.New(clierr.KindValidation, "invalid mode %q (use planning, triage or blocked)", mode)
		}

		ctx, prompt, err := planning.Generate(s, o.agent, mode)
		if err != nil {
			return classifyErr("plan generate failed", err)
		}

		stdout := cmd.OutOrStdout()
		fmt.Fprintln(stdout, prompt)

		if o.json {
			// machine-readable context snapshot follows the prompt
			data, err := json.MarshalIndent(ctx, "", "  ")
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "plan generate: failed to marshal context")
			}
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "--- context ---")
			fmt.Fprintln(stdout, string(data))
		}
		return nil
	}

	cmd.Flags().StringVar(&o.mode, "mode", planning.PromptModePlanning, "Prompt mode: planning, triage, blocked")
	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name to embed in the prompt")
	cmd.Flags().BoolVar(&o.json, "json", false, "Also print the machine-readable PlanningContext as JSON")
	return cmd
}

func newPlanSaveCmd(deps Dependencies) *cobra.Command {
	o := &planSaveOptions{}
	cmd := newCommand("save [PLAN-XXX]", "Parse an agent's plan text and persist it to .ctask/plans/",
		`Parse a plan returned by an agent and save it to .ctask/plans/PLAN-XXX.yaml.

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
  --agent         Record which agent generated this plan`)
	cmd.Args = maxNArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		if o.input == "" && o.file == "" {
			return clierr.New(clierr.KindUsage, "provide plan text via --input or --file")
		}
		if o.input != "" && o.file != "" {
			return clierr.New(clierr.KindUsage, "use either --input or --file, not both")
		}

		text := o.input
		if o.file != "" {
			data, err := os.ReadFile(o.file)
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to read plan file")
			}
			text = string(data)
		}

		proposal, err := planning.ParsePlanOutput(text)
		if err != nil {
			return classifyErr("plan save", err)
		}

		if len(args) == 1 {
			proposal.ID = args[0]
		}
		proposal.GeneratedBy = o.by
		if proposal.GeneratedBy == "" {
			proposal.GeneratedBy = o.agent
		}

		// Record the prompt used to generate this plan (audit channel).
		switch {
		case o.prompt != "" && o.promptFile != "":
			return clierr.New(clierr.KindUsage, "use either --prompt or --prompt-file, not both")
		case o.promptFile != "":
			data, err := os.ReadFile(o.promptFile)
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "failed to read prompt file")
			}
			proposal.PromptUsed = string(data)
		case o.prompt != "":
			proposal.PromptUsed = o.prompt
		}

		// Record the plan mode (planning / triage / blocked). Invalid values
		// are ignored, matching the lenient ParsePlanOutput philosophy.
		if o.mode != "" && isValidPlanMode(o.mode) {
			proposal.Mode = o.mode
		}

		if err := withProjectLock(deps, s, "plan save", o.agent, "", func() error {
			return planning.SavePlan(s, proposal)
		}); err != nil {
			return classifyErr("plan save failed", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Plan %s saved to %s\n", proposal.ID, s.PlanPath(proposal.ID))
		return nil
	}

	cmd.Flags().StringVar(&o.input, "input", "", "Plan text (recommendations + rationale)")
	cmd.Flags().StringVar(&o.file, "file", "", "Read plan text from a file")
	cmd.Flags().StringVar(&o.prompt, "prompt", "", "Full prompt text used to generate this plan")
	cmd.Flags().StringVar(&o.promptFile, "prompt-file", "", "Read the prompt text from a file")
	cmd.Flags().StringVar(&o.mode, "mode", "", "Record the plan mode: planning, triage, blocked")
	cmd.Flags().StringVar(&o.agent, "agent", "", "Agent name recorded as plan author")
	cmd.Flags().StringVar(&o.by, "by", "", "Alias for --agent on save")
	return cmd
}

func newPlanShowCmd(deps Dependencies) *cobra.Command {
	o := &planShowOptions{}
	cmd := newCommand("show <plan-id>", "Show a saved plan",
		`Display a previously saved plan from .ctask/plans/<plan-id>.yaml.

Use 'ctask plan list' to see all saved plans.

Flags:
  --prompt    Also print the full prompt that was used to generate this plan`)
	cmd.Args = exactArgs(1)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		id := args[0]
		plan, err := s.ReadPlan(id)
		if err != nil {
			return classifyErr("", err)
		}

		stdout := cmd.OutOrStdout()
		printPlan(stdout, plan)
		if o.prompt {
			if plan.PromptUsed != "" {
				fmt.Fprintln(stdout)
				fmt.Fprintln(stdout, "--- Prompt Used ---")
				fmt.Fprintln(stdout, plan.PromptUsed)
			} else {
				fmt.Fprintln(stdout)
				fmt.Fprintln(stdout, "(no prompt recorded for this plan)")
			}
		}
		return nil
	}

	cmd.Flags().BoolVar(&o.prompt, "prompt", false, "Also print the full prompt used for this plan")
	return cmd
}

func newPlanListCmd(deps Dependencies) *cobra.Command {
	o := &planListOptions{}
	cmd := newCommand("list", "List all saved plans",
		`List all plans saved in .ctask/plans/, newest first.

Flags:
  --json    Output as JSON`)
	cmd.Args = noArgs()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		s := newStore(deps)
		if err := requireInit(s); err != nil {
			return err
		}

		plans, err := s.ListPlans()
		if err != nil {
			return classifyErr("plan list failed", err)
		}

		stdout := cmd.OutOrStdout()
		if o.json {
			out, err := json.MarshalIndent(plans, "", "  ")
			if err != nil {
				return clierr.Wrap(clierr.KindOperation, err, "plan list: failed to marshal JSON")
			}
			fmt.Fprintln(stdout, string(out))
			return nil
		}

		if len(plans) == 0 {
			fmt.Fprintln(stdout, "No plans saved yet. Use 'ctask plan save' to record one.")
			return nil
		}

		// newest first
		for i := len(plans) - 1; i >= 0; i-- {
			p := plans[i]
			by := p.GeneratedBy
			if by == "" {
				by = "unknown"
			}
			fmt.Fprintf(stdout, "%s  %s  by %s  (%d recommendation(s))\n", p.ID, p.GeneratedAt, by, len(p.Recommendations))
		}
		return nil
	}

	cmd.Flags().BoolVar(&o.json, "json", false, "Output as JSON")
	return cmd
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
func printPlan(w io.Writer, p *model.PlanProposal) {
	fmt.Fprintf(w, "%s (generated %s", p.ID, p.GeneratedAt)
	if p.GeneratedBy != "" {
		fmt.Fprintf(w, " by %s", p.GeneratedBy)
	}
	fmt.Fprintln(w, ")")
	if len(p.Recommendations) == 0 {
		fmt.Fprintln(w, "  No recommendations.")
	}
	for _, r := range p.Recommendations {
		reason := r.Reason
		if reason == "" {
			reason = "(no reason)"
		}
		fmt.Fprintf(w, "  %s: %s | %s\n", r.TaskID, r.Action, reason)
	}
	if p.Rationale != "" {
		fmt.Fprintf(w, "\nRationale: %s\n", p.Rationale)
	}
}
