package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/model"
)

// setupProjectLockStore creates a temp dir and a store without initializing
// the project (AcquireProjectLock calls EnsureDir itself).
func setupProjectLockStore(t *testing.T) *Store {
	t.Helper()
	dir, err := os.MkdirTemp("", "codeledger-plock-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return NewStore(dir)
}

func TestAcquireProjectLock_CreatesLockFile(t *testing.T) {
	s := setupProjectLockStore(t)

	h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", TTL: time.Minute})
	if err != nil {
		t.Fatalf("AcquireProjectLock failed: %v", err)
	}
	defer h.Release()

	if _, err := os.Stat(s.ProjectLockPath()); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	lock, err := ReadProjectLock(s)
	if err != nil {
		t.Fatalf("ReadProjectLock failed: %v", err)
	}
	if lock == nil {
		t.Fatal("expected a project lock")
	}
	if lock.Command != "add" {
		t.Errorf("expected command add, got %q", lock.Command)
	}
	if lock.Pid != os.Getpid() {
		t.Errorf("expected pid %d, got %d", os.Getpid(), lock.Pid)
	}
	if lock.CreatedAt == "" || lock.ExpiresAt == "" {
		t.Errorf("expected created_at and expires_at, got %q / %q", lock.CreatedAt, lock.ExpiresAt)
	}

	// Lock event was recorded.
	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if !hasEventType(events, model.EventProjectLockAcquired) {
		t.Errorf("expected project.lock_acquired event, got %v", events)
	}
}

func TestReleaseProjectLock_RemovesLockFile(t *testing.T) {
	s := setupProjectLockStore(t)

	h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "done", TTL: time.Minute})
	if err != nil {
		t.Fatalf("AcquireProjectLock failed: %v", err)
	}

	if err := h.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	if _, err := os.Stat(s.ProjectLockPath()); !os.IsNotExist(err) {
		t.Errorf("lock file still exists after release: %v", err)
	}

	// Lock released event was recorded.
	events, _ := s.ReadEvents()
	if !hasEventType(events, model.EventProjectLockReleased) {
		t.Errorf("expected project.lock_released event, got %v", events)
	}

	// Releasing twice is a no-op.
	if err := h.Release(); err != nil {
		t.Errorf("double release should be a no-op, got: %v", err)
	}
}

func TestAcquireProjectLock_ActiveLockConflicts(t *testing.T) {
	s := setupProjectLockStore(t)

	h1, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer h1.Release()

	_, err = AcquireProjectLock(s, ProjectLockOptions{Command: "claim", TTL: 5 * time.Minute})
	if err == nil {
		t.Fatal("expected a conflict error for active lock")
	}
	if !IsProjectLockConflict(err) {
		t.Errorf("expected ProjectLockConflict, got: %v", err)
	}

	// Conflict event recorded.
	events, _ := s.ReadEvents()
	if !hasEventType(events, model.EventProjectLockConflict) {
		t.Errorf("expected project.lock_conflict event, got %v", events)
	}

	// The original lock file must still be intact (not overwritten).
	lock, err := ReadProjectLock(s)
	if err != nil {
		t.Fatalf("ReadProjectLock failed: %v", err)
	}
	if lock.Command != "add" {
		t.Errorf("original lock overwritten: command=%q", lock.Command)
	}
}

func TestAcquireProjectLock_ExpiredLockRemovedAndReacquired(t *testing.T) {
	s := setupProjectLockStore(t)

	h1, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", TTL: time.Minute})
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	// Simulate a crashed process that never released the lock:
	// rewrite the lock file with an already-expired expires_at.
	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	data, err := json.MarshalIndent(ProjectLock{
		Pid:       os.Getpid(),
		Command:   "add",
		Agent:     "",
		TaskID:    "",
		CreatedAt: time.Now().UTC().Add(-3 * time.Minute).Format(time.RFC3339),
		ExpiresAt: expired,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(s.ProjectLockPath(), append(data, '\n'), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	_ = h1

	// The lock is already expired, so a second acquire should succeed.
	h2, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim", TTL: time.Minute})
	if err != nil {
		t.Fatalf("acquire over expired lock failed: %v", err)
	}
	defer h2.Release()

	lock, err := ReadProjectLock(s)
	if err != nil {
		t.Fatalf("ReadProjectLock failed: %v", err)
	}
	if lock.Command != "claim" {
		t.Errorf("expected fresh lock command claim, got %q", lock.Command)
	}

	events, _ := s.ReadEvents()
	if !hasEventType(events, model.EventProjectLockStaleRemoved) {
		t.Errorf("expected project.lock_stale_removed event, got %v", events)
	}
	if !hasEventType(events, model.EventProjectLockAcquired) {
		t.Errorf("expected project.lock_acquired event, got %v", events)
	}
}

func TestAcquireProjectLock_DefaultTTL(t *testing.T) {
	s := setupProjectLockStore(t)

	h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "note"})
	if err != nil {
		t.Fatalf("AcquireProjectLock failed: %v", err)
	}
	defer h.Release()

	lock, err := ReadProjectLock(s)
	if err != nil {
		t.Fatalf("ReadProjectLock failed: %v", err)
	}
	expires, err := time.Parse(time.RFC3339, lock.ExpiresAt)
	if err != nil {
		t.Fatalf("invalid expires_at: %v", err)
	}
	// Default TTL is 2 minutes; allow small clock slack.
	if d := time.Until(expires); d < 2*time.Minute-time.Second || d > 2*time.Minute+time.Second {
		t.Errorf("expected ~2m TTL, got %v", d)
	}
}

func TestAcquireProjectLock_ConcurrentOnlyOneSucceeds(t *testing.T) {
	s := setupProjectLockStore(t)

	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	conflicts := 0

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim", TTL: 5 * time.Minute})
			if err != nil {
				mu.Lock()
				conflicts++
				mu.Unlock()
				return
			}
			// Hold briefly, then release.
			time.Sleep(50 * time.Millisecond)
			_ = h.Release()
			mu.Lock()
			successes++
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Because each lock is released quickly, later workers may acquire in turn.
	// The key invariant is that there is never a moment with two simultaneous
	// holders. We verify the strongest guarantee separately in
	// TestAcquireProjectLock_ConcurrentSimultaneousHolders.
	if successes == 0 {
		t.Fatalf("expected at least one successful acquisition, got 0 (conflicts=%d)", conflicts)
	}
	if successes+conflicts != workers {
		t.Errorf("accounting mismatch: successes=%d conflicts=%d workers=%d", successes, conflicts, workers)
	}
}

func TestAcquireProjectLock_ConcurrentSimultaneousHolders(t *testing.T) {
	s := setupProjectLockStore(t)

	// All workers try to acquire at the same time and HOLD the lock
	// (do not release). Exactly one must succeed.
	const workers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	conflicts := 0
	var handles []*ProjectLockHandle

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim", TTL: 5 * time.Minute})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				conflicts++
				return
			}
			successes++
			handles = append(handles, h)
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Errorf("expected exactly 1 successful acquisition, got %d (conflicts=%d)", successes, conflicts)
	}
	for _, h := range handles {
		_ = h.Release()
	}
}

func TestReadProjectLock_NoLockReturnsNil(t *testing.T) {
	s := setupProjectLockStore(t)

	lock, err := ReadProjectLock(s)
	if err != nil {
		t.Fatalf("ReadProjectLock failed: %v", err)
	}
	if lock != nil {
		t.Errorf("expected nil lock, got %+v", lock)
	}
}

func TestReadProjectLock_CorruptLockReclaimed(t *testing.T) {
	s := setupProjectLockStore(t)

	if err := os.MkdirAll(s.BasePath, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	// A corrupt lock file cannot be parsed.
	if err := os.WriteFile(s.ProjectLockPath(), []byte("not json {{"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// ReadProjectLock surfaces the parse error (does not mask it).
	if _, err := ReadProjectLock(s); err == nil {
		t.Fatal("expected an error reading a corrupt lock")
	}

	// Acquisition treats the corrupt lock as stale and reclaims it.
	h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", TTL: time.Minute})
	if err != nil {
		t.Fatalf("acquire over corrupt lock failed: %v", err)
	}
	defer h.Release()

	lock, err := ReadProjectLock(s)
	if err != nil {
		t.Fatalf("ReadProjectLock after reclaim failed: %v", err)
	}
	if lock.Command != "add" {
		t.Errorf("expected fresh lock command add, got %q", lock.Command)
	}

	events, _ := s.ReadEvents()
	if !hasEventType(events, model.EventProjectLockStaleRemoved) {
		t.Errorf("expected project.lock_stale_removed event for corrupt lock, got %v", events)
	}
}

func TestProjectLockFileModeIs0600(t *testing.T) {
	// Windows does not enforce POSIX permission bits: os.OpenFile's mode
	// argument is ignored and the reported file mode is always 0666, so the
	// 0600 assertion below can never pass there. The umask-independent 0600
	// creation mode is a POSIX-only guarantee; skip the permission check on
	// Windows and keep it for Unix-like platforms.
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits are not enforced on Windows (os.OpenFile mode is ignored)")
	}

	s := setupProjectLockStore(t)

	h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", TTL: time.Minute})
	if err != nil {
		t.Fatalf("AcquireProjectLock failed: %v", err)
	}
	defer h.Release()

	info, err := os.Stat(s.ProjectLockPath())
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}
}

func TestProjectLockPathInsideCtaskDir(t *testing.T) {
	s := setupProjectLockStore(t)
	if !strings.HasPrefix(filepath.ToSlash(s.ProjectLockPath()), filepath.ToSlash(s.BasePath)) {
		t.Errorf("project lock should live under .ctask dir, got %q", s.ProjectLockPath())
	}
	if filepath.Base(s.ProjectLockPath()) != ProjectLockFile {
		t.Errorf("expected lock file named %s, got %q", ProjectLockFile, filepath.Base(s.ProjectLockPath()))
	}
}

func TestRemoveWithRetry_RemovesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.tmp")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := removeWithRetry(path); err != nil {
		t.Fatalf("removeWithRetry failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after removeWithRetry: %v", err)
	}
}

func TestRemoveWithRetry_MissingFileIsSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.lock")

	// A missing file must be treated as success (os.ErrNotExist is not an error).
	if err := removeWithRetry(path); err != nil {
		t.Fatalf("removeWithRetry on missing file should succeed, got: %v", err)
	}
}

func TestRemoveWithRetry_ReturnsErrorWhenRemovalKeepsFailing(t *testing.T) {
	// A non-empty directory cannot be removed with os.Remove: the error is not
	// os.ErrNotExist and never resolves, so removeWithRetry must surface it
	// instead of silently swallowing it.
	dir := t.TempDir()
	nonEmpty := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(nonEmpty, "child"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	if err := removeWithRetry(nonEmpty); err == nil {
		t.Fatal("expected removeWithRetry to return an error for a non-empty directory")
	}
}

// hasEventType reports whether any event in the slice has the given type.
func hasEventType(events []model.Event, eventType string) bool {
	for _, e := range events {
		if e.Type == eventType {
			return true
		}
	}
	return false
}
