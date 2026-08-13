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

// deps returns the Dependencies bound to this environment's writers and dir.
func (e *testEnv) deps() Dependencies {
	return Dependencies{Stdin: e.In, Stdout: e.Out, Stderr: e.Err, WorkDir: e.Dir}
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
