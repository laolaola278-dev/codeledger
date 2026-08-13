package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// TestCmd_Locks_NoActiveLocks verifies `ctask locks` reports no locks.
func TestCmd_Locks_NoActiveLocks(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	out, err := env.run("locks")
	if err != nil {
		t.Fatalf("locks failed: %v", err)
	}
	if !contains(out, "No active locks.") {
		t.Errorf("expected 'No active locks.', got: %q", out)
	}
}

// TestCmd_Locks_ShowsTaskLocks verifies `ctask locks` lists task locks.
func TestCmd_Locks_ShowsTaskLocks(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	if _, err := env.run("add", "Task A"); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if _, err := env.run("claim", "TASK-001", "--agent", "codex", "--ttl", "30m"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	out, err := env.run("locks")
	if err != nil {
		t.Fatalf("locks failed: %v", err)
	}
	if !contains(out, "TASK-001") {
		t.Errorf("expected TASK-001 in locks output, got: %q", out)
	}
	if !contains(out, "codex") {
		t.Errorf("expected agent codex in locks output, got: %q", out)
	}
	if !contains(out, "Task locks") {
		t.Errorf("expected task locks section, got: %q", out)
	}
}

// TestCmd_Locks_ShowsProjectLock verifies `ctask locks` displays the
// project-level mutation lock while it is held.
func TestCmd_Locks_ShowsProjectLock(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	// Acquire the project lock directly, then inspect via `ctask locks`.
	s := env.store()
	handle, err := store.AcquireProjectLock(s, store.ProjectLockOptions{Command: "add", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatalf("AcquireProjectLock failed: %v", err)
	}
	defer handle.Release()

	out, err := env.run("locks")
	if err != nil {
		t.Fatalf("locks failed: %v", err)
	}
	if !contains(out, "Project mutation lock:") {
		t.Errorf("expected project mutation lock section, got: %q", out)
	}
	if !contains(out, "add") {
		t.Errorf("expected command add in project lock output, got: %q", out)
	}
	if !contains(out, "Expires:") {
		t.Errorf("expected expiry info in project lock output, got: %q", out)
	}
}

// TestCmd_Locks_JSON verifies the JSON output structure of `ctask locks --json`.
func TestCmd_Locks_JSON(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	out, err := env.run("locks", "--json")
	if err != nil {
		t.Fatalf("locks --json failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if parsed["no_active_locks"] != true {
		t.Errorf("expected no_active_locks=true, got %v", parsed["no_active_locks"])
	}
}

// TestCmd_MutationCommandBlockedByProjectLock verifies that a mutating
// command fails with a clear conflict error when the project lock is held.
func TestCmd_MutationCommandBlockedByProjectLock(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	// Hold the project lock manually.
	s := env.store()
	handle, err := store.AcquireProjectLock(s, store.ProjectLockOptions{Command: "other-agent", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatalf("AcquireProjectLock failed: %v", err)
	}
	defer handle.Release()

	out, err := env.run("add", "Task A")
	if err == nil {
		t.Fatal("expected add to fail while project lock is held")
	}
	if !strings.Contains(err.Error(), "project lock conflict") {
		t.Errorf("expected project lock conflict error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "other-agent") {
		t.Errorf("expected conflict message to name the lock holder, got: %v", err)
	}
	_ = out
}

// TestCmd_ConcurrentClaimOnlyOneSucceeds spawns many goroutines that all
// invoke the claim command against the same initialized project. Exactly one
// must win the task lock; the rest must fail with the claim-already-locked
// error, and the project lock must never be left behind. Each goroutine uses
// its own command tree, writers, and buffers - only the project directory is
// shared, so no package-level state is ever touched concurrently.
func TestCmd_ConcurrentClaimOnlyOneSucceeds(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	if _, err := env.run("add", "Task A"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	const workers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			deps := Dependencies{
				Stdin:   strings.NewReader(""),
				Stdout:  &bytes.Buffer{},
				Stderr:  &bytes.Buffer{},
				WorkDir: env.Dir,
			}
			root := NewRoot(deps)
			root.SetArgs([]string{"claim", "TASK-001", "--agent", fmt.Sprintf("agent-%d", i), "--ttl", "30m"})
			err := root.Execute()
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if successes != 1 {
		t.Errorf("expected exactly 1 successful claim, got %d", successes)
	}

	// The project lock must not be left behind after all claims completed.
	if _, err := os.Stat(filepath.Join(env.Dir, ".ctask", store.ProjectLockFile)); !os.IsNotExist(err) {
		t.Errorf("project lock file left behind: %v", err)
	}
}

// TestCmd_Locks_DoesNotAcquireProjectLock verifies the read-only locks
// command never creates a project lock file.
func TestCmd_Locks_DoesNotAcquireProjectLock(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	if _, err := env.run("locks"); err != nil {
		t.Fatalf("locks failed: %v", err)
	}

	s := env.store()
	if _, err := os.Stat(s.ProjectLockPath()); !os.IsNotExist(err) {
		t.Errorf("locks command must not create a project lock, found: %v", err)
	}
}

// TestProjectLockEventsRecorded verifies mutating commands record the
// project.lock_acquired and project.lock_released events.
func TestProjectLockEventsRecorded(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	if _, err := env.run("add", "Task A"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	s := env.store()
	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if !hasCmdEvent(events, model.EventProjectLockAcquired) {
		t.Errorf("expected project.lock_acquired event, got %v", events)
	}
	if !hasCmdEvent(events, model.EventProjectLockReleased) {
		t.Errorf("expected project.lock_released event, got %v", events)
	}
}

// hasCmdEvent reports whether any event has the given type.
func hasCmdEvent(events []model.Event, eventType string) bool {
	for _, e := range events {
		if e.Type == eventType {
			return true
		}
	}
	return false
}
