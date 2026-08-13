package store

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	DirName         = ".ctask"
	ProjectFile     = "project.yaml"
	TasksFile       = "tasks.yaml"
	DecisionsFile   = "decisions.md"
	ContextFile     = "context.md"
	EventsFile      = "events.jsonl"
	ProjectLockFile = ".ctask.lock"
	ReportsDir      = "reports"
	EvidenceDir     = "evidence"
)

// Store manages reading and writing to the .ctask directory.
type Store struct {
	BasePath string
}

// NewStore creates a Store rooted at the given base path.
// The .ctask subdirectory is appended automatically.
func NewStore(basePath string) *Store {
	return &Store{BasePath: filepath.Join(basePath, DirName)}
}

func (s *Store) ProjectPath() string     { return filepath.Join(s.BasePath, ProjectFile) }
func (s *Store) TasksPath() string       { return filepath.Join(s.BasePath, TasksFile) }
func (s *Store) DecisionsPath() string   { return filepath.Join(s.BasePath, DecisionsFile) }
func (s *Store) ContextPath() string     { return filepath.Join(s.BasePath, ContextFile) }
func (s *Store) EventsPath() string      { return filepath.Join(s.BasePath, EventsFile) }
func (s *Store) ProjectLockPath() string { return filepath.Join(s.BasePath, ProjectLockFile) }
func (s *Store) ReportsDirPath() string  { return filepath.Join(s.BasePath, ReportsDir) }
func (s *Store) LocksPath() string       { return filepath.Join(s.BasePath, "locks.yaml") }
func (s *Store) EvidenceDirPath() string { return filepath.Join(s.BasePath, EvidenceDir) }
func (s *Store) EvidenceRelPath(taskID string) string {
	return filepath.ToSlash(filepath.Join(EvidenceDir, taskID+".md"))
}
func (s *Store) EvidencePath(taskID string) string {
	return filepath.Join(s.EvidenceDirPath(), taskID+".md")
}
func (s *Store) EvidenceDiffPath(taskID string) string {
	return filepath.Join(s.EvidenceDirPath(), taskID+".diff")
}

func (s *Store) EvidenceDiffRelPath(taskID string) string {
	return filepath.ToSlash(filepath.Join(EvidenceDir, taskID+".diff"))
}

// IsInitialized checks whether the .ctask directory exists with a project.yaml.
func (s *Store) IsInitialized() bool {
	info, err := os.Stat(s.ProjectPath())
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// EnsureInitialized returns an error if the project is not initialized.
func (s *Store) RequireInit() error {
	if !s.IsInitialized() {
		return fmt.Errorf(".ctask not initialized. Run 'ctask init' first")
	}
	return nil
}

// EnsureDir creates the .ctask directory and its subdirectories if they don't exist.
func (s *Store) EnsureDir() error {
	if err := os.MkdirAll(s.BasePath, 0755); err != nil {
		return fmt.Errorf("failed to create .ctask directory: %w", err)
	}
	if err := os.MkdirAll(s.ReportsDirPath(), 0755); err != nil {
		return fmt.Errorf("failed to create reports directory: %w", err)
	}
	return nil
}

// EnsureEvidenceDir creates the evidence directory if it does not exist.
func (s *Store) EnsureEvidenceDir() error {
	if err := os.MkdirAll(s.EvidenceDirPath(), 0755); err != nil {
		return fmt.Errorf("failed to create evidence directory: %w", err)
	}
	return nil
}
