package model

import "time"

// Lock represents a task lease held by an agent.
//
// P1 lease contract:
//   - LeaseID is a unique per-acquisition identifier. A lock without a valid
//     LeaseID is a legacy lock (pre-P1 format) and is handled fail-closed by
//     the service layer: it blocks new claims, cannot be renewed or released
//     without an explicit --force --reason, and is surfaced by `ctask check`
//     as a warning.
//   - LeaseDuration records the duration (as parsed by time.ParseDuration)
//     used to compute ExpiresAt and to renew the lease on heartbeat.
//   - ExpiresAt is extended by the full LeaseDuration on every heartbeat:
//     heartbeat is a true lease renewal, not just a liveness stamp.
type Lock struct {
	TaskID        string `yaml:"task_id" json:"task_id"`
	Agent         string `yaml:"agent" json:"agent"`
	Role          string `yaml:"role" json:"role"`
	LeaseID       string `yaml:"lease_id,omitempty" json:"lease_id,omitempty"`
	LeaseDuration string `yaml:"lease_duration,omitempty" json:"lease_duration,omitempty"`
	AcquiredAt    string `yaml:"acquired_at" json:"acquired_at"`
	ExpiresAt     string `yaml:"expires_at" json:"expires_at"`
	HeartbeatAt   string `yaml:"heartbeat_at" json:"heartbeat_at"`
}

// LockList holds all locks.
type LockList struct {
	Locks []Lock `yaml:"locks" json:"locks"`
}

// Legacy reports whether the lock predates the P1 lease format and therefore
// cannot be trusted for strict owner/lease validation. A lock is legacy when
// it lacks a lease_id, or when any of its timestamps or its lease duration is
// missing or unparseable. Fail-closed: legacy locks are never silently
// treated as valid leases.
func (l *Lock) Legacy() bool {
	if l.LeaseID == "" {
		return true
	}
	if _, err := time.Parse(time.RFC3339, l.AcquiredAt); err != nil {
		return true
	}
	if _, err := time.Parse(time.RFC3339, l.ExpiresAt); err != nil {
		return true
	}
	if _, err := time.ParseDuration(l.LeaseDuration); err != nil {
		return true
	}
	return false
}

// ExpiredAt reports whether the lock's expires_at is before now.
// A missing or unparseable expires_at is treated as NOT expired so that the
// entry stays visible and fail-closed handling applies instead of the entry
// silently vanishing.
func (l *Lock) ExpiredAt(now time.Time) bool {
	if l.ExpiresAt == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, l.ExpiresAt)
	if err != nil {
		return false
	}
	return now.UTC().After(expiresAt)
}

// IsExpired reports whether the lock's expires_at is in the past according to
// the real system clock. Prefer ExpiredAt(now) with an injected clock for
// deterministic logic; this is retained for display and simple call sites.
func (l *Lock) IsExpired() bool {
	return l.ExpiredAt(time.Now())
}

// IsActiveAt reports whether the lock currently blocks a new claim: it is
// active when it has not expired, regardless of whether it is a legacy entry
// (fail-closed: legacy entries also block until explicitly cleared).
func (l *Lock) IsActiveAt(now time.Time) bool {
	return !l.ExpiredAt(now)
}

// IsValidLease reports whether the lock is a well-formed, still-valid lease:
// not legacy and not expired at now.
func (l *Lock) IsValidLease(now time.Time) bool {
	if l.Legacy() {
		return false
	}
	return !l.ExpiredAt(now)
}
