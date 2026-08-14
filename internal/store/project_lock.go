package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/gofrs/flock"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/model"
)

// DefaultProjectLockTTL is the default time-to-live recorded in the project
// lock metadata. With OS advisory locking the TTL is informational (display
// and audit); mutual exclusion is enforced by the kernel flock while the
// acquiring process lives, so a live holder can never be stolen and a dead
// holder's lock is released automatically by the OS.
const DefaultProjectLockTTL = 2 * time.Minute

// ProjectLock holds the JSON metadata written to .ctask/.ctask.lock.
//
// The file itself is the target of an OS advisory lock (flock(2) on Unix,
// LockFileEx on Windows) held by the acquiring process for the duration of a
// mutation. The JSON metadata is informational: it lets `ctask locks` and
// auditing tools see who holds the lock and when it expires.
type ProjectLock struct {
	Pid       int    `json:"pid"`
	Command   string `json:"command"`
	Agent     string `json:"agent"`
	TaskID    string `json:"task_id"`
	LeaseID   string `json:"lease_id,omitempty"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

// ProjectLockError is returned when a project lock cannot be acquired because
// another live process currently holds the advisory lock.
type ProjectLockError struct {
	Lock ProjectLock
}

func (e *ProjectLockError) Error() string {
	if e.Lock.Command == "" && e.Lock.Pid == 0 {
		return "project is locked by another process"
	}
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
// The handle owns the OS advisory lock: it keeps the *flock.Flock alive so
// the kernel holds the exclusive lock until Release (or process exit, which
// releases it automatically - a crash can never leave a stuck lock).
type ProjectLockHandle struct {
	s        *Store
	lock     ProjectLock
	fl       *flock.Flock
	acquired bool
	// appendEvent is the audit sink for the lock's own lifecycle events
	// (project.lock_acquired / project.lock_released). It defaults to
	// Store.AppendEvent and is an instance-level seam (never a global hook)
	// so deterministic failure tests can inject an audit outage.
	appendEvent func(evt model.Event) error
}

// Release clears the lock metadata, releases the OS advisory lock and closes
// the file descriptor. It is safe to call multiple times: once the lock is
// determinately released, subsequent calls are no-ops that never touch a
// later owner's lock file.
//
// Order: the project.lock_released event is written FIRST, then the metadata
// is cleared (while the OS lock is still held, so no reader can mistake the
// stale metadata for an active holder), then the lock is released.
//
// A failure in ANY step (audit append, metadata truncate, unlock, close) is
// collected and reported via errors.Join, but it never short-circuits the
// remaining cleanup steps: an audit or metadata failure must not leave the
// OS lock held. See releaseFlock for the exact state rule.
//
// The lock FILE is intentionally never unlinked: unlinking a lock file that
// another process may already have opened creates a classic race where two
// processes can each hold "the lock" on different inodes. The file persists
// as an empty placeholder after release, and an empty file means "no active
// lock" to every reader.
func (h *ProjectLockHandle) Release() error {
	if h == nil || !h.acquired {
		return nil
	}

	var errs []error
	evt := model.NewEvent(model.EventProjectLockReleased, "", "", "project lock released")
	if err := h.appendEvent(evt); err != nil {
		errs = append(errs, fmt.Errorf("failed to log project lock release: %w", err))
	}

	// Cleanup must run even when the audit append failed: releaseFlock
	// collects (rather than short-circuits on) every subsequent failure.
	released, err := releaseFlock(h.s, h.fl)
	if err != nil {
		errs = append(errs, err)
	}
	h.acquired = !released
	return errors.Join(errs...)
}

// releaseFlock best-effort clears the lock metadata and releases the OS
// advisory lock held by f, continuing through every step regardless of
// earlier failures. It never touches the audit sink, so it is safe to use on
// acquisition-failure paths where audit logging itself is broken.
//
// It reports whether the OS lock was determinately released.
//
// State rule (gofrs/flock v0.12.1): Flock.Unlock() calls flock(LOCK_UN) and,
// only on success, closes the file descriptor and resets the Flock to the
// unlocked state (a later Unlock/Close is then a no-op). On failure it
// returns without resetting, leaving the lock and fd held, so a retry
// re-attempts LOCK_UN. Flock.Close() is exactly Unlock(), so calling Unlock
// then Close yields one safe retry and a no-op when Unlock already
// succeeded. The lock is therefore determinately gone iff at least one of
// Unlock/Close returned nil; if both fail the outcome is unknown and the
// handle must remain "acquired" so a later Release retries cleanup.
func releaseFlock(s *Store, f *flock.Flock) (released bool, err error) {
	var errs []error
	if err := truncateProjectLockFile(s.ProjectLockPath()); err != nil {
		errs = append(errs, fmt.Errorf("failed to clear project lock metadata: %w", err))
	}
	unlockErr := f.Unlock()
	if unlockErr != nil {
		errs = append(errs, fmt.Errorf("failed to unlock project lock: %w", unlockErr))
	}
	closeErr := f.Close()
	if closeErr != nil {
		errs = append(errs, fmt.Errorf("failed to close project lock: %w", closeErr))
	}
	return unlockErr == nil || closeErr == nil, errors.Join(errs...)
}

// ProjectLockOptions configures acquisition of a project lock.
type ProjectLockOptions struct {
	Command string
	Agent   string
	TaskID  string
	TTL     time.Duration
	// Clock and NewID are injectable for deterministic tests; nil means
	// real clock and random IDs.
	Clock clock.Clock
	NewID lease.IDGen
	// AppendEvent overrides the audit sink for the project lock's own
	// lifecycle events (project.lock_acquired / project.lock_released /
	// project.lock_conflict / project.lock_stale_removed). It is nil in
	// production, which means Store.AppendEvent. Tests inject a deterministic
	// failure here; it is an instance-level seam, never a global hook or an
	// environment variable.
	AppendEvent func(evt model.Event) error
}

// AcquireProjectLock acquires the project mutation lock using a real OS
// advisory lock on .ctask/.ctask.lock (flock(2) on Unix, LockFileEx on
// Windows, via gofrs/flock). It returns a handle that must be released
// (ideally via defer) after the protected operation finishes.
//
// Return contract: success returns (non-nil handle, nil error); failure
// returns (nil handle, non-nil error). A held handle is never returned
// together with an error.
//
// Behavior:
//   - If no other live process holds the lock, acquisition succeeds
//     immediately and fresh metadata (including a new lease_id) is written.
//   - If another live process holds the lock, a ProjectLockError is returned
//     and a project.lock_conflict event is logged. A live holder is never
//     stolen, regardless of the recorded expiry: the OS lock is the source of
//     truth.
//   - Leftover metadata left by a crashed process (the OS released its lock
//     automatically) is reclaimed: a project.lock_stale_removed event is
//     logged and the metadata is overwritten. This also covers corrupt or
//     unreadable leftover files.
//   - If the audit append for any of the lock's own lifecycle events fails
//     after the flock has been taken, the OS resources are released through
//     the audit-independent releaseFlock path and (nil, error) is returned:
//     the lock is never leaked into the calling process.
func AcquireProjectLock(s *Store, opts ProjectLockOptions) (*ProjectLockHandle, error) {
	if opts.TTL <= 0 {
		opts.TTL = DefaultProjectLockTTL
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.RealClock{}
	}
	newID := opts.NewID
	if newID == nil {
		newID = lease.RandomID
	}
	appendEvent := opts.AppendEvent
	if appendEvent == nil {
		appendEvent = s.AppendEvent
	}

	if err := s.EnsureDir(); err != nil {
		return nil, fmt.Errorf("failed to ensure .ctask directory: %w", err)
	}

	f := flock.New(s.ProjectLockPath(),
		flock.SetFlag(os.O_CREATE|os.O_RDWR),
		flock.SetPermissions(0o600),
	)

	locked, err := f.TryLock()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire project lock: %w", err)
	}
	if !locked {
		// A live process holds the lock. Read whatever metadata is visible
		// for a helpful conflict message (best effort).
		existing, _ := readProjectLockFile(s)
		lock := ProjectLock{}
		if existing != nil {
			lock = *existing
		}
		conflictEvt := model.NewEvent(model.EventProjectLockConflict, "", "", fmt.Sprintf(
			"project lock conflict: held by pid %d (command %q, agent %q, task %q, expires %s)",
			lock.Pid, lock.Command, lock.Agent, lock.TaskID, lock.ExpiresAt))
		_ = appendEvent(conflictEvt)
		return nil, &ProjectLockError{Lock: lock}
	}

	// We now hold the OS lock. Any non-empty leftover metadata was left by a
	// process that died mid-mutation (or a corrupt/garbage file): reclaim it.
	existing, rerr := readProjectLockFile(s)
	switch {
	case rerr == nil && existing != nil:
		staleEvt := model.NewEvent(model.EventProjectLockStaleRemoved, "", "", fmt.Sprintf(
			"removed stale project lock from pid %d (command %q, expired %s)",
			existing.Pid, existing.Command, existing.ExpiresAt))
		if aerr := appendEvent(staleEvt); aerr != nil {
			_, cleanupErr := releaseFlock(s, f)
			return nil, errors.Join(
				fmt.Errorf("project lock stale removed but failed to log event: %w", aerr),
				cleanupErr,
			)
		}
	case rerr != nil:
		staleEvt := model.NewEvent(model.EventProjectLockStaleRemoved, "", "",
			"removed unreadable/corrupt project lock: "+rerr.Error())
		if aerr := appendEvent(staleEvt); aerr != nil {
			_, cleanupErr := releaseFlock(s, f)
			return nil, errors.Join(
				fmt.Errorf("project lock stale removed but failed to log event: %w", aerr),
				cleanupErr,
			)
		}
	}

	now := clk.Now().UTC()
	lock := ProjectLock{
		Pid:       os.Getpid(),
		Command:   opts.Command,
		Agent:     opts.Agent,
		TaskID:    opts.TaskID,
		LeaseID:   newID(),
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(opts.TTL).Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		_, cleanupErr := releaseFlock(s, f)
		return nil, errors.Join(fmt.Errorf("failed to marshal project lock: %w", err), cleanupErr)
	}
	if err := writeProjectLockFile(s, append(data, '\n')); err != nil {
		_, cleanupErr := releaseFlock(s, f)
		return nil, errors.Join(fmt.Errorf("failed to write project lock metadata: %w", err), cleanupErr)
	}

	// Acquire contract: success returns (non-nil handle, nil error); failure
	// returns (nil handle, non-nil error). A held handle is never handed to a
	// caller alongside an error.
	handle := &ProjectLockHandle{s: s, lock: lock, fl: f, acquired: true, appendEvent: appendEvent}
	evt := model.NewEvent(model.EventProjectLockAcquired, "", "", fmt.Sprintf(
		"project lock acquired for %q (pid %d, lease %s)", opts.Command, lock.Pid, lock.LeaseID))
	if aerr := appendEvent(evt); aerr != nil {
		// The OS lock is held but the audit append failed. Release the OS
		// resources through the audit-independent path (never a normal
		// Release(), which would append another event via the same broken
		// sink) and return nil handle + combined error.
		_, cleanupErr := releaseFlock(s, f)
		return nil, errors.Join(
			fmt.Errorf("project lock acquired but failed to log event: %w", aerr),
			cleanupErr,
		)
	}
	return handle, nil
}

// writeProjectLockFile truncates and rewrites the lock metadata while the
// caller holds the OS lock.
func writeProjectLockFile(s *Store, data []byte) error {
	f, err := os.OpenFile(s.ProjectLockPath(), os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// truncateProjectLockFile clears the lock metadata, leaving an empty file.
func truncateProjectLockFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}

// readProjectLockFile reads and parses .ctask/.ctask.lock.
// An empty (released) file returns (nil, nil): no active lock.
func readProjectLockFile(s *Store) (*ProjectLock, error) {
	data, err := os.ReadFile(s.ProjectLockPath())
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var lock ProjectLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("failed to parse project lock: %w", err)
	}
	return &lock, nil
}

// ReadProjectLock reads the current project lock metadata, if any.
// It returns nil when there is no lock file or the file is empty (released).
// Corrupt non-empty files surface an error (they are reclaimed on next
// acquisition).
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
