package contextgen

import (
	"fmt"
	"strings"

	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
)

// GenerateContext generates a Markdown context summary suitable for AI coding agents.
func GenerateContext(s *store.Store) (string, error) {
	p, err := s.ReadProject()
	if err != nil {
		return "", err
	}

	status, err := service.GetStatus(s)
	if err != nil {
		return "", err
	}

	recentDone, err := service.GetRecentDoneTasks(s, 5)
	if err != nil {
		return "", err
	}

	files, err := service.GetModifiedFiles(s)
	if err != nil {
		return "", err
	}

	testResults, err := service.GetTestResults(s)
	if err != nil {
		return "", err
	}

	decisions, err := s.ReadDecisions()
	if err != nil {
		decisions = "No decisions recorded yet."
	}

	var b strings.Builder

	// Header
	b.WriteString("# Project Context\n\n")

	// Project Goal
	b.WriteString("## Project Goal\n\n")
	if p.Goal != "" {
		b.WriteString(p.Goal + "\n\n")
	} else {
		b.WriteString("_No goal defined yet._\n\n")
	}

	// Current Progress
	b.WriteString("## Current Progress\n\n")
	b.WriteString(fmt.Sprintf("- **Total Tasks:** %d\n", status.Total))
	b.WriteString(fmt.Sprintf("- **Pending:** %d\n", status.Pending))
	b.WriteString(fmt.Sprintf("- **In Progress:** %d\n", status.InProgress))
	b.WriteString(fmt.Sprintf("- **Done:** %d\n", status.Done))
	b.WriteString(fmt.Sprintf("- **Blocked:** %d\n", status.Blocked))
	b.WriteString("\n")

	// Current Task
	b.WriteString("## Current Task\n\n")
	if status.CurrentTask != nil {
		t := status.CurrentTask
		b.WriteString(fmt.Sprintf("**%s:** %s (%s)\n\n", t.ID, t.Title, t.Priority))
		if t.Description != "" {
			b.WriteString(t.Description + "\n\n")
		}
		if len(t.DependsOn) > 0 {
			b.WriteString(fmt.Sprintf("Depends on: %s\n\n", strings.Join(t.DependsOn, ", ")))
		}
	} else {
		b.WriteString("_No task is currently in progress._\n\n")
	}

	// Next Suggested Task
	b.WriteString("## Next Suggested Task\n\n")
	if status.NextTask != nil {
		t := status.NextTask
		b.WriteString(fmt.Sprintf("**%s:** %s (%s)\n", t.ID, t.Title, t.Priority))
		if t.Description != "" {
			b.WriteString(t.Description + "\n")
		}
		if len(t.DependsOn) > 0 {
			b.WriteString(fmt.Sprintf("Depends on: %s\n", strings.Join(t.DependsOn, ", ")))
		}
	} else {
		b.WriteString("_All pending tasks have unmet dependencies, or no tasks remain._\n")
	}
	b.WriteString("\n")

	// Recent Done Tasks
	if len(recentDone) > 0 {
		b.WriteString("## Recently Completed Tasks\n\n")
		for _, t := range recentDone {
			b.WriteString(fmt.Sprintf("- **%s:** %s\n", t.ID, t.Title))
			if len(t.Files) > 0 {
				b.WriteString(fmt.Sprintf("  - Files: %s\n", strings.Join(t.Files, ", ")))
			}
			if t.Test.Command != "" {
				result := t.Test.Result
				if result == "" {
					result = "unknown"
				}
				b.WriteString(fmt.Sprintf("  - Test: `%s` ->**%s**\n", t.Test.Command, result))
			}
			if strings.Join(t.Evidence, ", ") != "" {
				b.WriteString(fmt.Sprintf("  - Evidence: `%s`\n", strings.Join(t.Evidence, ", ")))
			}
		}
		b.WriteString("\n")
	}

	// Blocked Tasks
	if len(status.BlockedTasks) > 0 {
		b.WriteString("## Blocked Tasks\n\n")
		for _, t := range status.BlockedTasks {
			b.WriteString(fmt.Sprintf("- **%s:** %s\n", t.ID, t.Title))
			if t.BlockedReason != "" {
				b.WriteString(fmt.Sprintf("  - Reason: %s\n", t.BlockedReason))
			}
		}
		b.WriteString("\n")
	}

	// Important Decisions
	b.WriteString("## Important Decisions\n\n")
	if decisions != "" && decisions != "No decisions recorded yet." {
		// Only include the decision entries, not the header boilerplate
		lines := strings.Split(decisions, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "Record") && !strings.HasPrefix(trimmed, "Each entry") {
				b.WriteString(line + "\n")
			}
		}
		b.WriteString("\n")
	} else {
		b.WriteString("_No decisions recorded yet._\n\n")
	}

	// Modified Files
	if len(files) > 0 {
		b.WriteString("## Modified Files\n\n")
		b.WriteString("```\n")
		for _, f := range files {
			b.WriteString(f + "\n")
		}
		b.WriteString("```\n\n")
	}

	// Test Results
	if len(testResults) > 0 {
		b.WriteString("## Test Results\n\n")
		for _, t := range testResults {
			result := t.Test.Result
			if result == "" {
				result = "unknown"
			}
			b.WriteString(fmt.Sprintf("- **%s** `%s`: %s\n", t.ID, t.Test.Command, result))
		}
		b.WriteString("\n")
	}

	// Agent Instructions
	b.WriteString("## Agent Instructions\n\n")
	b.WriteString("Before starting work:\n")
	b.WriteString("1. Read this context to understand the current state.\n")
	b.WriteString("2. Select a pending task and run `ctask start <TASK-ID>`.\n")
	b.WriteString("3. Use `ctask note <TASK-ID>` to record important findings.\n\n")
	b.WriteString("After modifying code:\n")
	b.WriteString("1. Run relevant tests.\n")
	b.WriteString("2. Use `ctask done <TASK-ID>` with `--files`, `--test`, and `--result`.\n")
	b.WriteString("3. If blocked, use `ctask block <TASK-ID> \"reason\"`.\n")
	b.WriteString("4. Record architectural decisions in `.ctask/decisions.md`.\n\n")
	b.WriteString("Never:\n")
	b.WriteString("- Delete or modify `.ctask/` directory structure.\n")
	b.WriteString("- Falsify test results.\n")
	b.WriteString("- Make large changes without updating task status.\n")
	b.WriteString("- End a session without running `ctask status` and `ctask context`.\n")

	return b.String(), nil
}
