package model

import (
	"testing"
	"time"
)

func TestLockIsExpired_EmptyExpiresAt(t *testing.T) {
	l := Lock{TaskID: "TASK-001", Agent: "test"}
	if l.IsExpired() {
		t.Error("lock with empty expires_at should not be expired")
	}
}

func TestLockIsExpired_FutureExpiry(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	l := Lock{TaskID: "TASK-001", Agent: "test", ExpiresAt: future}
	if l.IsExpired() {
		t.Error("lock with future expires_at should not be expired")
	}
}

func TestLockIsExpired_PastExpiry(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	l := Lock{TaskID: "TASK-001", Agent: "test", ExpiresAt: past}
	if !l.IsExpired() {
		t.Error("lock with past expires_at should be expired")
	}
}

func TestLockIsExpired_InvalidFormat(t *testing.T) {
	l := Lock{TaskID: "TASK-001", Agent: "test", ExpiresAt: "not-a-valid-time"}
	if l.IsExpired() {
		t.Error("lock with invalid expires_at format should not be treated as expired")
	}
}

func TestLockList_Empty(t *testing.T) {
	ll := LockList{Locks: []Lock{}}
	if len(ll.Locks) != 0 {
		t.Error("empty lock list should have 0 locks")
	}
}

func TestLockList_WithLocks(t *testing.T) {
	ll := LockList{
		Locks: []Lock{
			{TaskID: "TASK-001", Agent: "agent1"},
			{TaskID: "TASK-002", Agent: "agent2"},
		},
	}
	if len(ll.Locks) != 2 {
		t.Errorf("expected 2 locks, got %d", len(ll.Locks))
	}
}
