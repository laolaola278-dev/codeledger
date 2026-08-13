package store

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/codeledger/codeledger/internal/model"
)

// ReadLocks reads the locks.yaml file.
func (s *Store) ReadLocks() (*model.LockList, error) {
	data, err := os.ReadFile(s.LocksPath())
	if err != nil {
		return nil, fmt.Errorf("failed to read locks: %w", err)
	}
	var ll model.LockList
	if err := yaml.Unmarshal(data, &ll); err != nil {
		return nil, fmt.Errorf("failed to parse locks.yaml: %w", err)
	}
	return &ll, nil
}

// WriteLocks writes the locks.yaml file.
func (s *Store) WriteLocks(ll *model.LockList) error {
	data, err := yaml.Marshal(ll)
	if err != nil {
		return fmt.Errorf("failed to marshal locks: %w", err)
	}
	if err := os.WriteFile(s.LocksPath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write locks.yaml: %w", err)
	}
	return nil
}

// EnsureLocksFile creates an empty locks.yaml if it does not exist.
func (s *Store) EnsureLocksFile() error {
	if _, err := os.Stat(s.LocksPath()); os.IsNotExist(err) {
		emptyLocks := model.LockList{Locks: []model.Lock{}}
		return s.WriteLocks(&emptyLocks)
	}
	return nil
}
