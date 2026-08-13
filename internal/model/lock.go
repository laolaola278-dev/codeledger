package model

import "time"

// Lock represents a task lock held by an agent.
type Lock struct {
	TaskID      string `yaml:"task_id" json:"task_id"`
	Agent       string `yaml:"agent" json:"agent"`
	Role        string `yaml:"role" json:"role"`
	AcquiredAt  string `yaml:"acquired_at" json:"acquired_at"`
	ExpiresAt   string `yaml:"expires_at" json:"expires_at"`
	HeartbeatAt string `yaml:"heartbeat_at" json:"heartbeat_at"`
}

// LockList holds all locks.
type LockList struct {
	Locks []Lock `yaml:"locks" json:"locks"`
}

// IsExpired returns true if the lock's expires_at is in the past.
// An empty expires_at is treated as not expired.
func (l *Lock) IsExpired() bool {
	if l.ExpiresAt == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, l.ExpiresAt)
	if err != nil {
		return false
	}
	return time.Now().After(expiresAt)
}
