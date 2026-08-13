package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/codeledger/codeledger/internal/model"
)

// DefaultProjectLockTTL is the default time-to-live for a project lock.
// A stale lock older than this TTL is considered expired and can be removed.
const DefaultProjectLockTTL = 2 * time.Minute

// ProjectLock holds the JSON metadata written to .ctask/.ctask.lock.
type ProjectLock struct {
	Pid       int    `json:"pid"`
	Command   string `json:"command"`
	Agent     string `json:"agent"`
	TaskID    string `json:"task_id"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

// ProjectLockError is returned when a project lock cannot be acquired because
// another (non-expired) lock is already held.
type ProjectLockError struct {
	Lock ProjectLock
}

func (e *ProjectLockError) Error() string {
	return fmt.Sprintf("project is locked by another process (pid %d, command %q, agent %q, task %q, expires %s)",
		e.Lock.Pid, e.Lock.Command, e.Lock.Agent, e.Lock.TaskID, e.Lock.ExpiresAt)
}

// IsProjectLockConflict reports whether err is a project lock conflict.
func IsProjectLockConflict(err error) bool {
	var ple *ProjectLockError
	return errors.As(err, &ple)
}

// ProjectLockHandle represents a held project lock. Callers must call Release
// (ideally via defer) once the protected operation completes.
//
// It deliberately does NOT hold an open *os.File: the lock file is created,
// written, synced and closed inside AcquireProjectLock, so on Windows there is
// no lingering handle that could make os.Remove fail with
// "being used by another process". The handle only tracks the store, the lock
// metadata and the released state.
type ProjectLockHandle struct {
	s        *Store
	lock     ProjectLock
	acquired bool
}

// Release removes the project lock file if it is still owned by this handle.
// It is safe to call multiple times: once released, subsequent calls are no-ops.
//
// Order: the project.lock_released event is written FIRST, then the lock file
// is removed with a short Windows-friendly retry. If the file is already gone
// (os.ErrNotExist) the release is still considered successful. The handle is
// marked released only after the file removal actually succeeds; if removal
// ultimately fails, an error is returned and the handle stays acquired so the
// caller can retry (and the error is never silently swallowed).
func (h *ProjectLockHandle) Release() error {
	if h == nil || !h.acquired {
		return nil
	}

	// Record the release event BEFORE removing the lock file so that the
	// events.jsonl timeline never shows the lock file disappearing before its
	// project.lock_released event was written. Readers that only observe the
	// event log (e.g. `ctask locks` or auditing tools) must see a consistent
	// order: the release is logged first, then the file is actually removed.
	evt := model.NewEvent(model.EventProjectLockReleased, "", "", "project lock released")
	if err := h.s.AppendEvent(evt); err != nil {
		return err
	}

	if err := removeWithRetry(h.s.ProjectLockPath()); err != nil {
		return fmt.Errorf("failed to remove project lock: %w", err)
	}
	h.acquired = false
	return nil
}

// ProjectLockOptions configures acquisition of a project lock.
type ProjectLockOptions struct {
	Command string
	Agent   string
	TaskID  string
	TTL     time.Duration
}

// AcquireProjectLock creates .ctask/.ctask.lock atomically using
// os.OpenFile with O_CREATE|O_EXCL. It returns a handle that must be
// released (ideally via defer) after the protected operation finishes.
//
// Behavior:
//   - If the lock file does not exist, it is created with JSON metadata and a
//     project.lock_acquired event is logged.
//   - If the lock file exists and has NOT expired, a ProjectLockError is
//     returned and a project.lock_conflict event is logged.
//   - If the lock file exists but HAS expired, the stale lock is removed, a
//     project.lock_stale_removed event is logged, and a fresh lock is acquired.
func AcquireProjectLock(s *Store, opts ProjectLockOptions) (*ProjectLockHandle, error) {
	if opts.TTL <= 0 {
		opts.TTL = DefaultProjectLockTTL
	}

	now := time.Now().UTC()
	lock := ProjectLock{
		Pid:       os.Getpid(),
		Command:   opts.Command,
		Agent:     opts.Agent,
		TaskID:    opts.TaskID,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(opts.TTL).Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal project lock: %w", err)
	}

	if err := s.EnsureDir(); err != nil {
		return nil, fmt.Errorf("failed to ensure .ctask directory: %w", err)
	}

	f, err := os.OpenFile(s.ProjectLockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		if _, werr := f.Write(append(data, '\n')); werr != nil {
			f.Close()
			_ = removeWithRetry(s.ProjectLockPath())
			return nil, fmt.Errorf("failed to write project lock: %w", werr)
		}
		// Sync then close immediately so no open handle is retained. On Windows,
		// an open file handle can make a later os.Remove fail with
		// "The process cannot access the file because it is being used by
		// another process"; closing here guarantees Release can delete the file.
		_ = f.Sync()
		if cerr := f.Close(); cerr != nil {
			_ = removeWithRetry(s.ProjectLockPath())
			return nil, fmt.Errorf("failed to close project lock: %w", cerr)
		}

		evt := model.NewEvent(model.EventProjectLockAcquired, "", "", fmt.Sprintf("project lock acquired for %q (pid %d)", opts.Command, lock.Pid))
		if aerr := s.AppendEvent(evt); aerr != nil {
			// The lock is held; report the event failure but keep the lock.
			return &ProjectLockHandle{s: s, lock: lock, acquired: true}, fmt.Errorf("project lock acquired but failed to log event: %w", aerr)
		}
		return &ProjectLockHandle{s: s, lock: lock, acquired: true}, nil
	}

	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("failed to create project lock: %w", err)
	}

	// Lock file already exists: inspect it.
	existing, rerr := readProjectLockFile(s)
	if rerr == nil && !isProjectLockExpired(existing.ExpiresAt) {
		conflictEvt := model.NewEvent(model.EventProjectLockConflict, "", "", fmt.Sprintf(
			"project lock conflict: held by pid %d (command %q, agent %q, task %q, expires %s)",
			existing.Pid, existing.Command, existing.Agent, existing.TaskID, existing.ExpiresAt))
		_ = s.AppendEvent(conflictEvt)
		return nil, &ProjectLockError{Lock: *existing}
	}

	if errors.Is(rerr, os.ErrNotExist) {
		// The lock file vanished between our O_EXCL failure and this read:
		// a concurrent process removed it. Retry acquisition.
		return AcquireProjectLock(s, opts)
	}

	// At this point the existing lock is either expired (stale) or unreadable/corrupt.
	// Remove it so mutations cannot be blocked forever, then re-acquire.
	if rerr != nil {
		staleEvt := model.NewEvent(model.EventProjectLockStaleRemoved, "", "",
			"removed unreadable/corrupt project lock: "+rerr.Error())
		if aerr := s.AppendEvent(staleEvt); aerr != nil {
			return nil, fmt.Errorf("project lock stale removed but failed to log event: %w", aerr)
		}
	} else {
		staleEvt := model.NewEvent(model.EventProjectLockStaleRemoved, "", "", fmt.Sprintf(
			"removed stale project lock from pid %d (expired %s)", existing.Pid, existing.ExpiresAt))
		if aerr := s.AppendEvent(staleEvt); aerr != nil {
			return nil, fmt.Errorf("project lock stale removed but failed to log event: %w", aerr)
		}
	}
	if err := removeWithRetry(s.ProjectLockPath()); err != nil {
		return nil, fmt.Errorf("failed to remove stale project lock: %w", err)
	}
	return AcquireProjectLock(s, opts)
}

// removeWithRetry removes path, retrying briefly on transient failures.
//
// On Windows, os.Remove of a file that was just closed by the same process (or
// briefly held by an external handle, e.g. an antivirus scanner) can fail with
// ERROR_SHARING_VIOLATION / "The process cannot access the file because it is
// being used by another process". Such failures are often transient and vanish
// within a few milliseconds, so this helper retries with a short backoff
// (50ms, 100ms, 150ms, 200ms, 250ms) before giving up. A missing file is
// treated as success (os.ErrNotExist).
func removeWithRetry(path string) error {
	var err error
	for i := 0; i < 5; i++ {
		err = os.Remove(path)
		if err == nil {
			return nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
	}
	return err
}

// readProjectLockFile reads and parses .ctask/.ctask.lock.
func readProjectLockFile(s *Store) (*ProjectLock, error) {
	data, err := os.ReadFile(s.ProjectLockPath())
	if err != nil {
		return nil, err
	}
	var lock ProjectLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("failed to parse project lock: %w", err)
	}
	return &lock, nil
}

// isProjectLockExpired returns true when the expires_at timestamp is in the past.
// A missing or unparseable expires_at is treated as expired (safe to reclaim).
func isProjectLockExpired(expiresAt string) bool {
	if expiresAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return time.Now().UTC().After(t)
}

// ReadProjectLock reads the current project lock, if any.
// It returns nil when no lock file exists.
func ReadProjectLock(s *Store) (*ProjectLock, error) {
	lock, err := readProjectLockFile(s)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return lock, nil
}
