package service

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/codeledger/codeledger/internal/util"
)

const defaultDecisionsContent = `# Decisions
 
 Record important architectural and design decisions here.
 
 Each entry should include:
 - Date
 - Context (what prompted the decision)
 - Decision (what was decided)
 - Consequences (what this means going forward)
 `

// InitProject initializes the .ctask directory with default files.
func InitProject(s *store.Store) error {
	if s.IsInitialized() {
		return fmt.Errorf(".ctask already initialized (run from a different directory or remove .ctask to start over)")
	}

	if err := s.EnsureDir(); err != nil {
		return err
	}

	// Create empty locks.yaml if not exists
	if err := s.EnsureLocksFile(); err != nil {
		return err
	}

	now := util.NowRFC3339()
	p := model.DefaultProject()
	p.CreatedAt = now
	p.UpdatedAt = now
	if err := s.WriteProject(&p); err != nil {
		return err
	}

	emptyTasks := model.TaskList{Tasks: []model.Task{}}
	if err := s.WriteTasks(&emptyTasks); err != nil {
		return err
	}

	if err := os.WriteFile(s.DecisionsPath(), []byte(defaultDecisionsContent), 0644); err != nil {
		return fmt.Errorf("failed to create decisions.md: %w", err)
	}

	if err := os.WriteFile(s.ContextPath(), []byte("# Project Context\n\nRun `ctask context` to generate.\n"), 0644); err != nil {
		return fmt.Errorf("failed to create context.md: %w", err)
	}

	// Initialize empty events file
	f, err := os.Create(s.EventsPath())
	if err != nil {
		return fmt.Errorf("failed to create events.jsonl: %w", err)
	}
	f.Close()

	return nil
}

// UpdateProjectGoal updates the project's goal field and timestamp.
func UpdateProjectGoal(s *store.Store, goal string) error {
	p, err := s.ReadProject()
	if err != nil {
		return err
	}
	p.Goal = goal
	p.UpdatedAt = util.NowRFC3339()
	return s.WriteProject(p)
}

// AddDecision appends an architectural decision to decisions.md and records it in the event log.
func AddDecision(s *store.Store, decision, context, consequences string) error {
	date := time.Now().Format("2006-01-02")

	var b strings.Builder
	b.WriteString("\n## ")
	b.WriteString(date)
	b.WriteString("\n\n")
	b.WriteString("- **Context:** ")
	b.WriteString(context)
	b.WriteString("\n")
	b.WriteString("- **Decision:** ")
	b.WriteString(decision)
	b.WriteString("\n")
	b.WriteString("- **Consequences:** ")
	b.WriteString(consequences)
	b.WriteString("\n")

	if err := s.AppendDecision(b.String()); err != nil {
		return err
	}

	evt := model.NewEvent(model.EventDecisionAdded, "", decision, context)
	return s.AppendEvent(evt)
}
