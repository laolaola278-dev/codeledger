package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

func TestExecute_TextErrorRenderedOnce(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	code := Execute(context.Background(), env.deps(), []string{"start", "TASK-999"})
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if n := strings.Count(env.Err.String(), "Error:"); n != 1 {
		t.Errorf("expected exactly one rendered error, got %d in %q", n, env.Err.String())
	}
	if env.Out.Len() != 0 {
		t.Errorf("expected no stdout output for a text-mode error, got %q", env.Out.String())
	}
}

func TestExecute_JSONErrorEnvelope(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	// Corrupt the fixture so check reports failures.
	s := env.store()
	tl, _ := s.ReadTasks()
	tl.Tasks = append(tl.Tasks, model.Task{ID: "TASK-001", Title: "x", Status: "invalid", Priority: model.PriorityMedium})
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}

	code := Execute(context.Background(), env.deps(), []string{"check", "--json"})
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}

	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(env.Out.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not a valid JSON envelope: %v\noutput: %s", err, env.Out.String())
	}
	if envelope.OK {
		t.Error("expected ok=false in error envelope")
	}
	if envelope.Error.Code != "CHECK_FAILED" {
		t.Errorf("expected stable error code CHECK_FAILED, got %q", envelope.Error.Code)
	}
	if envelope.Error.Message == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecute_SuccessPrintsNoErrorEnvelope(t *testing.T) {
	env := newTestEnv(t)

	code := Execute(context.Background(), env.deps(), []string{"init"})
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if strings.Contains(env.Out.String(), `"ok"`) {
		t.Errorf("success output must not contain an error envelope: %q", env.Out.String())
	}
	if env.Err.Len() != 0 {
		t.Errorf("expected empty stderr on success, got %q", env.Err.String())
	}
}

func TestExecute_UsageErrorDoesNotDumpHelp(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	code := Execute(context.Background(), env.deps(), []string{"add"})
	if code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(env.Err.String(), "accepts 1 arg(s), received 0") {
		t.Errorf("expected arg-count error on stderr, got %q", env.Err.String())
	}
	if strings.Contains(env.Err.String(), "Usage:") || strings.Contains(env.Out.String(), "Usage:") {
		t.Error("usage help must not be dumped on a usage error")
	}
}

func TestExecute_UnknownFlagIsUsage(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	code := Execute(context.Background(), env.deps(), []string{"add", "x", "--bogus"})
	if code != 2 {
		t.Errorf("expected exit code 2 for unknown flag, got %d", code)
	}
	if !strings.Contains(env.Err.String(), "unknown flag: --bogus") {
		t.Errorf("expected unknown flag error on stderr, got %q", env.Err.String())
	}
}

func TestExecute_UnknownCommandIsUsage(t *testing.T) {
	env := newTestEnv(t)

	code := Execute(context.Background(), env.deps(), []string{"bogus-command"})
	if code != 2 {
		t.Errorf("expected exit code 2 for unknown command, got %d", code)
	}
	if !strings.Contains(env.Err.String(), "unknown command") {
		t.Errorf("expected unknown command error on stderr, got %q", env.Err.String())
	}
}

func TestExecute_NotInitializedExitCode(t *testing.T) {
	env := newTestEnv(t)

	code := Execute(context.Background(), env.deps(), []string{"status"})
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(env.Err.String(), ".ctask not initialized") {
		t.Errorf("expected not-initialized error on stderr, got %q", env.Err.String())
	}
}

func TestExecute_LockConflictExitCode(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	// A live holder: the OS advisory lock is the source of truth in P1, so a
	// real flock held by another open file description is required to produce
	// a conflict (leftover metadata alone is reclaimed, not a conflict).
	s := env.store()
	handle, err := store.AcquireProjectLock(s, store.ProjectLockOptions{
		Command: "other-agent",
		Agent:   "other-agent",
		TTL:     5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("AcquireProjectLock failed: %v", err)
	}
	defer handle.Release()

	code := Execute(context.Background(), env.deps(), []string{"add", "Task A"})
	if code != 3 {
		t.Errorf("expected exit code 3 for lock conflict, got %d", code)
	}
	if !strings.Contains(env.Err.String(), "project lock conflict") {
		t.Errorf("expected lock conflict on stderr, got %q", env.Err.String())
	}
}
