package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// kindOf returns the stable machine kind of the error returned by env.run,
// failing the test if it is not a typed CLI error.
func kindOf(t *testing.T, err error) clierr.Kind {
	t.Helper()
	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected typed clierr.Error, got: %v", err)
	}
	return ce.Kind
}

func TestCommandErrorKinds(t *testing.T) {
	t.Run("task not found", func(t *testing.T) {
		env := newTestEnv(t)
		env.initProject()
		_, err := env.run("start", "TASK-999")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := kindOf(t, err); got != clierr.KindNotFound {
			t.Errorf("expected NOT_FOUND, got %q", got)
		}
	})

	t.Run("not initialized", func(t *testing.T) {
		env := newTestEnv(t)
		_, err := env.run("status")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := kindOf(t, err); got != clierr.KindNotInitialized {
			t.Errorf("expected NOT_INITIALIZED, got %q", got)
		}
	})

	t.Run("invalid priority", func(t *testing.T) {
		env := newTestEnv(t)
		env.initProject()
		_, err := env.run("add", "Task", "--priority", "urgent")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := kindOf(t, err); got != clierr.KindValidation {
			t.Errorf("expected VALIDATION_ERROR, got %q", got)
		}
	})

	t.Run("missing argument is usage", func(t *testing.T) {
		env := newTestEnv(t)
		env.initProject()
		_, err := env.run("add")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := kindOf(t, err); got != clierr.KindUsage {
			t.Errorf("expected USAGE_ERROR, got %q", got)
		}
	})

	t.Run("project lock conflict", func(t *testing.T) {
		env := newTestEnv(t)
		env.initProject()
		s := env.store()
		handle, err := store.AcquireProjectLock(s, store.ProjectLockOptions{Command: "other-agent", TTL: 5 * time.Minute})
		if err != nil {
			t.Fatalf("AcquireProjectLock failed: %v", err)
		}
		defer handle.Release()

		_, err = env.run("add", "Task")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := kindOf(t, err); got != clierr.KindLockConflict {
			t.Errorf("expected LOCK_CONFLICT, got %q", got)
		}
	})

	t.Run("check failure", func(t *testing.T) {
		env := newTestEnv(t)
		env.initProject()
		if _, err := env.run("add", "Task A"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		if _, err := env.run("done", "TASK-001"); err != nil {
			t.Fatalf("done failed: %v", err)
		}
		// A done task with no files/test result/evidence produces warnings,
		// not failures; use a corrupt fixture for a real failure.
		s := env.store()
		tl, err := s.ReadTasks()
		if err != nil {
			t.Fatalf("ReadTasks failed: %v", err)
		}
		tl.Tasks[0].Status = "invalid-status"
		if err := s.WriteTasks(tl); err != nil {
			t.Fatalf("WriteTasks failed: %v", err)
		}
		_, err = env.run("check")
		if err == nil {
			t.Fatal("expected check failure")
		}
		if got := kindOf(t, err); got != clierr.KindCheckFailed {
			t.Errorf("expected CHECK_FAILED, got %q", got)
		}
	})
}

func TestCommandErrorKindsStrictCheck(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	// Blocked task with no reason -> warning -> --strict fails.
	s := env.store()
	tl, _ := s.ReadTasks()
	tl.Tasks = append(tl.Tasks, blockedNoReasonTask())
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}
	if _, err := env.run("check", "--strict"); err == nil {
		t.Fatal("expected strict check failure on warning")
	}
}

func TestCheckWithoutStrictIgnoresWarnings(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	s := env.store()
	tl, _ := s.ReadTasks()
	tl.Tasks = append(tl.Tasks, blockedNoReasonTask())
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}
	if _, err := env.run("check"); err != nil {
		t.Errorf("expected check to pass without --strict, got: %v", err)
	}
}

// blockedNoReasonTask returns a blocked task without a reason, which the
// consistency check flags as a warning.
func blockedNoReasonTask() model.Task {
	return model.Task{ID: "TASK-001", Title: "Blocked no reason", Status: model.StatusBlocked, Priority: model.PriorityMedium}
}
