package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/util"
)

// PlanDir is the name of the directory holding generated plans.
//
//	.ctask/
//	  plans/
//	    PLAN-001.yaml
const PlanDir = "plans"

// PlanDirPath returns .ctask/plans/.
func (s *Store) PlanDirPath() string {
	return filepath.Join(s.BasePath, PlanDir)
}

// PlanPath returns .ctask/plans/<id>.yaml.
func (s *Store) PlanPath(id string) string {
	return filepath.Join(s.PlanDirPath(), id+".yaml")
}

// EnsurePlanDir creates the plans directory if it does not exist.
func (s *Store) EnsurePlanDir() error {
	return os.MkdirAll(s.PlanDirPath(), 0755)
}

// NextPlanID generates the next plan ID (e.g. PLAN-002 after PLAN-001).
// It scans the existing plan files on disk; if none exist, returns PLAN-001.
func (s *Store) NextPlanID() (string, error) {
	entries, err := os.ReadDir(s.PlanDirPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "PLAN-001", nil
		}
		return "", fmt.Errorf("failed to list plans directory: %w", err)
	}

	maxNum := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "PLAN-") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		var num int
		if _, err := fmt.Sscanf(name, "PLAN-%d.yaml", &num); err != nil {
			continue
		}
		if num > maxNum {
			maxNum = num
		}
	}
	return fmt.Sprintf("PLAN-%03d", maxNum+1), nil
}

// SavePlan persists a plan proposal to .ctask/plans/<id>.yaml.
// If the plan has no ID yet, one is generated automatically.
func (s *Store) SavePlan(plan *model.PlanProposal) error {
	if plan == nil {
		return fmt.Errorf("cannot save nil plan")
	}
	if plan.ID == "" {
		id, err := s.NextPlanID()
		if err != nil {
			return err
		}
		plan.ID = id
	}
	if plan.GeneratedAt == "" {
		plan.GeneratedAt = util.NowRFC3339()
	}
	if err := s.EnsurePlanDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(plan)
	if err != nil {
		return fmt.Errorf("failed to marshal plan: %w", err)
	}
	if err := os.WriteFile(s.PlanPath(plan.ID), data, 0644); err != nil {
		return fmt.Errorf("failed to write plan: %w", err)
	}
	return nil
}

// ReadPlan reads a single plan by ID.
func (s *Store) ReadPlan(id string) (*model.PlanProposal, error) {
	data, err := os.ReadFile(s.PlanPath(id))
	if err != nil {
		return nil, fmt.Errorf("failed to read plan %s: %w", id, err)
	}
	var plan model.PlanProposal
	if err := yaml.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("failed to parse plan %s: %w", id, err)
	}
	return &plan, nil
}

// ListPlans returns all plans sorted by ID (PLAN-001 first).
func (s *Store) ListPlans() ([]model.PlanProposal, error) {
	entries, err := os.ReadDir(s.PlanDirPath())
	if err != nil {
		if os.IsNotExist(err) {
			return []model.PlanProposal{}, nil
		}
		return nil, fmt.Errorf("failed to list plans directory: %w", err)
	}

	var plans []model.PlanProposal
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "PLAN-") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		id := strings.TrimSuffix(name, ".yaml")
		plan, err := s.ReadPlan(id)
		if err != nil {
			continue // skip unreadable plans instead of failing the whole list
		}
		plans = append(plans, *plan)
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].ID < plans[j].ID
	})
	return plans, nil
}
