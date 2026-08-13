package store

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/codeledger/codeledger/internal/model"
)

// ReadProject reads the project.yaml file.
func (s *Store) ReadProject() (*model.Project, error) {
	data, err := os.ReadFile(s.ProjectPath())
	if err != nil {
		return nil, fmt.Errorf("failed to read project: %w", err)
	}
	var p model.Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse project.yaml: %w", err)
	}
	return &p, nil
}

// WriteProject writes the project.yaml file.
func (s *Store) WriteProject(p *model.Project) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("failed to marshal project: %w", err)
	}
	if err := os.WriteFile(s.ProjectPath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write project.yaml: %w", err)
	}
	return nil
}

// ReadTasks reads the tasks.yaml file.
func (s *Store) ReadTasks() (*model.TaskList, error) {
	data, err := os.ReadFile(s.TasksPath())
	if err != nil {
		return nil, fmt.Errorf("failed to read tasks: %w", err)
	}
	var tl model.TaskList
	if err := yaml.Unmarshal(data, &tl); err != nil {
		return nil, fmt.Errorf("failed to parse tasks.yaml: %w", err)
	}
	return &tl, nil
}

// WriteTasks writes the tasks.yaml file.
func (s *Store) WriteTasks(tl *model.TaskList) error {
	data, err := yaml.Marshal(tl)
	if err != nil {
		return fmt.Errorf("failed to marshal tasks: %w", err)
	}
	if err := os.WriteFile(s.TasksPath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write tasks.yaml: %w", err)
	}
	return nil
}

// ReadDecisions reads the decisions.md file.
func (s *Store) ReadDecisions() (string, error) {
	data, err := os.ReadFile(s.DecisionsPath())
	if err != nil {
		return "", fmt.Errorf("failed to read decisions: %w", err)
	}
	return string(data), nil
}

// AppendDecision appends a new decision entry to decisions.md.
func (s *Store) AppendDecision(content string) error {
	f, err := os.OpenFile(s.DecisionsPath(), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open decisions.md: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write to decisions.md: %w", err)
	}
	return nil
}

// WriteContext writes the context.md file.
func (s *Store) WriteContext(content string) error {
	if err := os.WriteFile(s.ContextPath(), []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write context.md: %w", err)
	}
	return nil
}
