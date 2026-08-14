package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
)

// testEnv is a self-contained CLI execution environment: independent writers
// and an independent working directory, so every execution builds a fresh
// command tree with no shared package state.
type testEnv struct {
	t   *testing.T
	Dir string
	Out *bytes.Buffer
	Err *bytes.Buffer
	In  io.Reader
	// Clock and NewID are optional injectable dependencies for deterministic
	// lease/lock tests; when nil the default real clock / random IDs apply.
	Clock clock.Clock
	NewID lease.IDGen
	// LockAudit overrides the project lock's lifecycle-event audit sink;
	// nil means Store.AppendEvent (production default). Tests inject a
	// deterministic failure here.
	LockAudit func(model.Event) error
}

// newTestEnv creates an isolated environment with its own temp working dir.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return &testEnv{
		t:   t,
		Dir: t.TempDir(),
		Out: &bytes.Buffer{},
		Err: &bytes.Buffer{},
		In:  strings.NewReader(""),
	}
}

// deps returns the Dependencies bound to this environment's writers and dir,
// including any injected clock / ID generator / lock audit sink.
func (e *testEnv) deps() Dependencies {
	return Dependencies{
		Stdin:     e.In,
		Stdout:    e.Out,
		Stderr:    e.Err,
		WorkDir:   e.Dir,
		Clock:     e.Clock,
		NewID:     e.NewID,
		LockAudit: e.LockAudit,
	}
}

// run executes a freshly built command tree with the given args and returns
// the captured stdout. Each call builds a new root, so flag values can never
// leak between executions.
func (e *testEnv) run(args ...string) (string, error) {
	e.t.Helper()
	e.Out.Reset()
	e.Err.Reset()
	root := NewRoot(e.deps())
	root.SetArgs(args)
	err := root.Execute()
	return e.Out.String(), err
}

// initProject initializes .ctask in the environment's working directory.
func (e *testEnv) initProject() {
	e.t.Helper()
	if err := service.InitProject(store.NewStore(e.Dir)); err != nil {
		e.t.Fatalf("InitProject failed: %v", err)
	}
}

// store returns a Store rooted at the environment's working directory.
func (e *testEnv) store() *store.Store {
	return store.NewStore(e.Dir)
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// writeLegacyLock plants a pre-P1 lock entry (no lease_id, no
// lease_duration) into locks.yaml so lease commands exercise the fail-closed
// legacy path. The project must already be initialized.
func writeLegacyLock(t *testing.T, env *testEnv, taskID, agent string) {
	t.Helper()
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	locks := &model.LockList{Locks: []model.Lock{
		{
			TaskID:     taskID,
			Agent:      agent,
			Role:       "developer",
			AcquiredAt: t0.Format(time.RFC3339),
			ExpiresAt:  t0.Add(2 * time.Hour).Format(time.RFC3339),
		},
	}}
	if err := env.store().WriteLocks(locks); err != nil {
		t.Fatalf("WriteLocks failed: %v", err)
	}
}

// assertNoActiveProjectLock fails the test if dir still has an active
// project mutation lock. P1 keeps the lock FILE as an empty placeholder
// after release (it is never unlinked, to avoid the classic unlink race), so
// "released" means ReadProjectLock returns nil - not file absence.
func assertNoActiveProjectLock(t *testing.T, dir string) {
	t.Helper()
	lock, err := store.ReadProjectLock(store.NewStore(dir))
	if err != nil {
		t.Fatalf("ReadProjectLock failed: %v", err)
	}
	if lock != nil {
		t.Errorf("expected project lock to be released, found active lock: %+v", lock)
	}
}

// writeProjectLockFixture writes a non-expired .ctask/.ctask.lock owned by
// another process into dir, so a following mutation is blocked with a lock
// conflict. The project must already be initialized.
func writeProjectLockFixture(t *testing.T, dir string) {
	t.Helper()
	lock := store.ProjectLock{
		Pid:       424242,
		Command:   "other-agent",
		Agent:     "other-agent",
		TaskID:    "TASK-001",
		CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339),
	}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatalf("marshal lock fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ctask", store.ProjectLockFile), data, 0600); err != nil {
		t.Fatalf("write lock fixture: %v", err)
	}
}
