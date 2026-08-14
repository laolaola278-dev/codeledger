package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/model"
)

// TestAcquire_AcquiredEventFailureDoesNotLeak covers the F1 acquire path: a
// failed project.lock_acquired append must return (nil handle, error) and
// must release the OS lock so a later acquire succeeds in the same process.
func TestAcquire_AcquiredEventFailureDoesNotLeak(t *testing.T) {
	s := setupProjectLockStore(t)

	h, err := AcquireProjectLock(s, ProjectLockOptions{
		Command:     "add",
		AppendEvent: func(model.Event) error { return errors.New("audit down") },
	})
	if h != nil {
		t.Fatalf("expected nil handle on failure, got %+v", h)
	}
	if err == nil {
		t.Fatal("expected an error when the acquired event append fails")
	}
	if !strings.Contains(err.Error(), "audit down") {
		t.Errorf("expected audit failure in error, got: %v", err)
	}

	// Same process, no exit, no sentinel deletion, no TTL wait: the failure
	// path must have released the flock.
	h2, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim"})
	if err != nil {
		t.Fatalf("re-acquire after acquired-event failure leaked the flock: %v", err)
	}
	if h2 == nil {
		t.Fatal("expected a handle on successful re-acquire")
	}
	_ = h2.Release()
}

// TestRelease_ReleasedEventFailureStillUnlocks covers the F1 release path: a
// failed project.lock_released append is reported, but the lock is still
// released; the owner's second Release is idempotent and must not disturb a
// later owner.
func TestRelease_ReleasedEventFailureStillUnlocks(t *testing.T) {
	s := setupProjectLockStore(t)
	appendEvent := func(evt model.Event) error {
		if evt.Type == model.EventProjectLockReleased {
			return errors.New("release audit down")
		}
		return s.AppendEvent(evt)
	}

	h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", AppendEvent: appendEvent})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	relErr := h.Release()
	if relErr == nil {
		t.Fatal("expected release to report the audit failure")
	}
	if !strings.Contains(relErr.Error(), "release audit down") {
		t.Errorf("expected release audit failure in error, got: %v", relErr)
	}

	// B acquires immediately in the same process.
	h2, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim"})
	if err != nil {
		t.Fatalf("B could not acquire after A's release-audit failure: %v", err)
	}

	// A's second Release is an idempotent no-op and must not disturb B.
	if err := h.Release(); err != nil {
		t.Errorf("second release should be a no-op, got: %v", err)
	}

	// C still conflicts while B holds the lock.
	_, err = AcquireProjectLock(s, ProjectLockOptions{Command: "note"})
	if !IsProjectLockConflict(err) {
		t.Errorf("expected a conflict while B holds the lock, got: %v", err)
	}
	_ = h2.Release()
}

// TestRelease_MultipleCleanupErrorsStillUnlocks injects a release-audit
// failure AND a real filesystem metadata-cleanup failure, and proves the OS
// lock is still released and every observable failure is reported.
func TestRelease_MultipleCleanupErrorsStillUnlocks(t *testing.T) {
	s := setupProjectLockStore(t)
	appendEvent := func(evt model.Event) error {
		if evt.Type == model.EventProjectLockReleased {
			return errors.New("release audit down")
		}
		return s.AppendEvent(evt)
	}

	h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", AppendEvent: appendEvent})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// Force the metadata truncate to fail with a real filesystem error: turn
	// the lock file into a directory (EISDIR even when running as root).
	if err := os.Remove(s.ProjectLockPath()); err != nil {
		t.Fatalf("remove lock file: %v", err)
	}
	if err := os.MkdirAll(s.ProjectLockPath(), 0o755); err != nil {
		t.Fatalf("mkdir over lock path: %v", err)
	}

	relErr := h.Release()
	if relErr == nil {
		t.Fatal("expected release to report failures")
	}
	if !strings.Contains(relErr.Error(), "release audit down") {
		t.Errorf("expected release audit failure, got: %v", relErr)
	}
	if !strings.Contains(relErr.Error(), "failed to clear project lock metadata") {
		t.Errorf("expected metadata cleanup failure, got: %v", relErr)
	}

	// The flock on the (now unlinked) inode was still released: after
	// restoring the lock path, a new acquire succeeds in the same process.
	if err := os.Remove(s.ProjectLockPath()); err != nil {
		t.Fatalf("remove lock-path dir: %v", err)
	}
	h2, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim"})
	if err != nil {
		t.Fatalf("re-acquire after release with cleanup failures: %v", err)
	}
	_ = h2.Release()
}

// TestAcquire_ReturnOwnershipInvariant verifies that no failure stage of the
// project-lock acquisition ever returns a held (non-nil) handle alongside an
// error.
func TestAcquire_ReturnOwnershipInvariant(t *testing.T) {
	t.Run("ensure-dir failure", func(t *testing.T) {
		dir := t.TempDir()
		// A regular file at .ctask makes EnsureDir fail.
		if err := os.WriteFile(filepath.Join(dir, ".ctask"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write blocker file: %v", err)
		}
		h, err := AcquireProjectLock(NewStore(dir), ProjectLockOptions{Command: "add"})
		if h != nil || err == nil {
			t.Fatalf("expected (nil, err) on ensure-dir failure, got handle=%v err=%v", h != nil, err)
		}
	})

	t.Run("open/flock failure", func(t *testing.T) {
		s := setupProjectLockStore(t)
		// A directory at the lock path makes flock open fail (EISDIR).
		if err := os.MkdirAll(s.ProjectLockPath(), 0o755); err != nil {
			t.Fatalf("mkdir lock path: %v", err)
		}
		h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add"})
		if h != nil || err == nil {
			t.Fatalf("expected (nil, err) on open failure, got handle=%v err=%v", h != nil, err)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		s := setupProjectLockStore(t)
		h1, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add"})
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		defer h1.Release()
		h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim"})
		if h != nil {
			t.Fatalf("expected nil handle on conflict, got %+v", h)
		}
		if !IsProjectLockConflict(err) {
			t.Fatalf("expected ProjectLockError, got %v", err)
		}
	})

	t.Run("acquired-event failure", func(t *testing.T) {
		s := setupProjectLockStore(t)
		h, err := AcquireProjectLock(s, ProjectLockOptions{
			Command:     "add",
			AppendEvent: func(model.Event) error { return errors.New("audit down") },
		})
		if h != nil || err == nil {
			t.Fatalf("expected (nil, err) on acquired-event failure, got handle=%v err=%v", h != nil, err)
		}
	})
}

// TestProjectLock_RealFilesystemRepro reproduces the independent review's
// EISDIR reproducer: replacing events.jsonl with a directory makes the audit
// append fail for real, and the lock must still be recoverable in-process.
func TestProjectLock_RealFilesystemRepro(t *testing.T) {
	t.Run("acquired event failure then reacquire", func(t *testing.T) {
		s := setupProjectLockStore(t)
		if err := os.MkdirAll(s.EventsPath(), 0o755); err != nil {
			t.Fatalf("mkdir events path: %v", err)
		}
		h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add"})
		if h != nil || err == nil {
			t.Fatalf("expected (nil, err), got handle=%v err=%v", h != nil, err)
		}
		if err := os.Remove(s.EventsPath()); err != nil {
			t.Fatalf("remove events dir: %v", err)
		}
		h2, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim"})
		if err != nil {
			t.Fatalf("re-acquire after audit failure: %v", err)
		}
		_ = h2.Release()
	})

	t.Run("release event failure then reacquire", func(t *testing.T) {
		s := setupProjectLockStore(t)
		h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add"})
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if err := os.Remove(s.EventsPath()); err != nil {
			t.Fatalf("remove events file: %v", err)
		}
		if err := os.MkdirAll(s.EventsPath(), 0o755); err != nil {
			t.Fatalf("mkdir events path: %v", err)
		}
		relErr := h.Release()
		if relErr == nil {
			t.Fatal("expected release audit failure")
		}
		if err := os.Remove(s.EventsPath()); err != nil {
			t.Fatalf("remove events dir: %v", err)
		}
		h2, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim"})
		if err != nil {
			t.Fatalf("re-acquire after release audit failure: %v", err)
		}
		_ = h2.Release()
	})
}

// TestProjectLockHelperProcess is a re-exec helper used by
// TestProjectLock_SurvivesAfterAuditFailure. It performs the requested
// acquire/release-with-error path and then stays alive (signalled via a
// marker file) so the parent can prove the flock is no longer held while this
// process is still running.
func TestProjectLockHelperProcess(t *testing.T) {
	mode := os.Getenv("CTASK_FLOCK_HELPER_MODE")
	if mode == "" {
		return
	}
	dir := os.Getenv("CTASK_FLOCK_HELPER_DIR")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "helper: missing CTASK_FLOCK_HELPER_DIR")
		os.Exit(9)
	}
	s := NewStore(dir)
	ready := filepath.Join(dir, "helper-ready")
	done := filepath.Join(dir, "helper-done")

	switch mode {
	case "acquire-fail":
		h, err := AcquireProjectLock(s, ProjectLockOptions{
			Command:     "add",
			AppendEvent: func(model.Event) error { return errors.New("audit down") },
		})
		if h != nil || err == nil {
			fmt.Fprintln(os.Stderr, "helper: expected acquire failure")
			os.Exit(9)
		}
	case "release-fail":
		appendEvent := func(evt model.Event) error {
			if evt.Type == model.EventProjectLockReleased {
				return errors.New("release audit down")
			}
			return s.AppendEvent(evt)
		}
		h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "add", AppendEvent: appendEvent})
		if err != nil {
			fmt.Fprintf(os.Stderr, "helper: acquire failed: %v\n", err)
			os.Exit(9)
		}
		if err := h.Release(); err == nil {
			fmt.Fprintln(os.Stderr, "helper: expected release failure")
			os.Exit(9)
		}
	default:
		fmt.Fprintf(os.Stderr, "helper: unknown mode %q\n", mode)
		os.Exit(9)
	}

	if err := os.WriteFile(ready, []byte("1"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "helper: write ready: %v\n", err)
		os.Exit(9)
	}
	for i := 0; i < 400; i++ { // up to ~20s
		if _, err := os.Stat(done); err == nil {
			os.Exit(0)
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "helper: timed out waiting for done")
	os.Exit(9)
}

// TestProjectLock_SurvivesAfterAuditFailure runs a live helper subprocess
// through each F1 error path and verifies that, while the helper process is
// still alive, the parent can acquire the same lock (i.e. the error did not
// leave the flock held).
func TestProjectLock_SurvivesAfterAuditFailure(t *testing.T) {
	for _, mode := range []string{"acquire-fail", "release-fail"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".ctask"), 0o755); err != nil {
				t.Fatalf("mkdir .ctask: %v", err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestProjectLockHelperProcess$")
			cmd.Env = append(os.Environ(),
				"CTASK_FLOCK_HELPER_MODE="+mode,
				"CTASK_FLOCK_HELPER_DIR="+dir,
			)
			if err := cmd.Start(); err != nil {
				t.Fatalf("start helper: %v", err)
			}
			defer func() {
				_ = os.WriteFile(filepath.Join(dir, "helper-done"), []byte("1"), 0o644)
				_ = cmd.Wait()
			}()

			ready := filepath.Join(dir, "helper-ready")
			deadline := time.Now().Add(20 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("timed out waiting for helper readiness")
				}
				time.Sleep(20 * time.Millisecond)
			}

			// While the helper is still alive, the parent must be able to
			// acquire the lock.
			s := NewStore(dir)
			h, err := AcquireProjectLock(s, ProjectLockOptions{Command: "claim"})
			if err != nil {
				t.Fatalf("parent could not acquire while helper alive: %v", err)
			}
			_ = h.Release()
		})
	}
}
