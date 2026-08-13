package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// testBin is the path of the real ctask binary built once in TestMain.
var testBin string

func TestMain(m *testing.M) {
	code := buildTestBinary()
	if code != 0 {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// buildTestBinary compiles the real ctask binary into a temp dir so the
// subprocess tests exercise the actual process boundary (exit codes,
// stdout/stderr split, JSON envelopes). Failing loudly here surfaces full
// build output instead of silently skipping.
func buildTestBinary() int {
	root := repoRoot()
	if root == "" {
		fmt.Fprintln(os.Stderr, "binary tests: repo root not found (go.mod missing); skipping binary tests")
		return 0
	}
	dir, err := os.MkdirTemp("", "ctask-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "binary tests: MkdirTemp: %v\n", err)
		return 1
	}
	bin := filepath.Join(dir, "ctask")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/codeledger/codeledger")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "binary tests: failed to build ctask: %v\n%s", err, out)
		return 1
	}
	testBin = bin
	return 0
}

// repoRoot walks up from the test working directory to find go.mod.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// binResult captures a subprocess run.
type binResult struct {
	code   int
	stdout string
	stderr string
}

// runBin runs the built ctask binary in dir with args and returns the exit
// code plus captured streams. On failure the full streams are logged so
// failures are diagnosable.
func runBin(t *testing.T, dir string, args ...string) binResult {
	t.Helper()
	if testBin == "" {
		t.Skip("ctask binary not built")
	}
	cmd := exec.Command(testBin, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("failed to run %s %v: %v", testBin, args, err)
		}
	}
	return binResult{code: code, stdout: out.String(), stderr: errb.String()}
}

func TestBinary_AddMissingArg_Exit2(t *testing.T) {
	dir := t.TempDir()
	r := runBin(t, dir, "add")
	if r.code != 2 {
		t.Errorf("expected exit 2, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "accepts 1 arg(s), received 0") {
		t.Errorf("expected arg-count error on stderr, got: %q", r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("expected empty stdout, got: %q", r.stdout)
	}
}

func TestBinary_NotInitialized_Exit1(t *testing.T) {
	dir := t.TempDir()
	r := runBin(t, dir, "status")
	if r.code != 1 {
		t.Errorf("expected exit 1, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, ".ctask not initialized") {
		t.Errorf("expected not-initialized error on stderr, got: %q", r.stderr)
	}
}

func TestBinary_StartMissingTask_Exit1(t *testing.T) {
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	r := runBin(t, dir, "start", "TASK-999")
	if r.code != 1 {
		t.Errorf("expected exit 1, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "TASK-999") {
		t.Errorf("expected task id in error, got: %q", r.stderr)
	}
}

func TestBinary_AddInvalidPriority_Exit2AndNoTaskAdded(t *testing.T) {
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	r := runBin(t, dir, "add", "x", "--priority", "urgent")
	if r.code != 2 {
		t.Errorf("expected exit 2, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "invalid priority") {
		t.Errorf("expected invalid priority error on stderr, got: %q", r.stderr)
	}
	tl, err := store.NewStore(dir).ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks failed: %v", err)
	}
	if len(tl.Tasks) != 0 {
		t.Errorf("expected no task added after invalid priority, got %d task(s)", len(tl.Tasks))
	}
	// The project lock must have been released despite the failure.
	if _, err := os.Stat(filepath.Join(dir, ".ctask", store.ProjectLockFile)); !os.IsNotExist(err) {
		t.Errorf("project lock left behind: %v", err)
	}
}

func TestBinary_ProjectLockConflict_Exit3(t *testing.T) {
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	writeProjectLockFixture(t, dir)

	r := runBin(t, dir, "add", "Task A")
	if r.code != 3 {
		t.Errorf("expected exit 3, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "project lock conflict") {
		t.Errorf("expected lock conflict error on stderr, got: %q", r.stderr)
	}
	if !strings.Contains(r.stderr, "other-agent") {
		t.Errorf("expected conflict to name the lock holder, got: %q", r.stderr)
	}
}

func TestBinary_SuccessPath_Exit0(t *testing.T) {
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	r := runBin(t, dir, "add", "Task A")
	if r.code != 0 {
		t.Errorf("add failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "TASK-001") {
		t.Errorf("expected TASK-001 in add output, got: %q", r.stdout)
	}
	r = runBin(t, dir, "list")
	if r.code != 0 {
		t.Errorf("list failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "TASK-001") {
		t.Errorf("expected TASK-001 in list output, got: %q", r.stdout)
	}
	if r.stderr != "" {
		t.Errorf("expected empty stderr on success, got: %q", r.stderr)
	}
}

func TestBinary_CheckStrict_Exit1WithCleanDefer(t *testing.T) {
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	// Warning fixture: blocked task without a reason.
	s := store.NewStore(dir)
	tl, err := s.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks failed: %v", err)
	}
	tl.Tasks = append(tl.Tasks, model.Task{
		ID: "TASK-001", Title: "Blocked no reason", Status: model.StatusBlocked, Priority: model.PriorityMedium,
	})
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}

	// Plain check on warnings exits 0.
	if r := runBin(t, dir, "check"); r.code != 0 {
		t.Errorf("plain check should pass warnings with exit 0, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}

	// --strict turns warnings into exit 1.
	r := runBin(t, dir, "check", "--strict")
	if r.code != 1 {
		t.Errorf("expected exit 1 for strict check, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}

	// The check event must still have been appended: the process exited via
	// the normal error path (no os.Exit skipping deferred/queued work).
	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if !hasCmdEvent(events, model.EventCheckPassed) && !hasCmdEvent(events, model.EventCheckFailed) {
		t.Errorf("expected a check.* event to be appended, got %v", events)
	}
}

func TestBinary_CheckJSONFailure_ValidEnvelope(t *testing.T) {
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	// Failure fixture: task with an invalid status.
	s := store.NewStore(dir)
	tl, err := s.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks failed: %v", err)
	}
	tl.Tasks = append(tl.Tasks, model.Task{
		ID: "TASK-001", Title: "x", Status: "invalid", Priority: model.PriorityMedium,
	})
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}

	r := runBin(t, dir, "check", "--json")
	if r.code != 1 {
		t.Errorf("expected exit 1, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}

	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &envelope); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput:\n%s", err, r.stdout)
	}
	if envelope.OK {
		t.Error("expected ok=false in error envelope")
	}
	if envelope.Error.Code != "CHECK_FAILED" {
		t.Errorf("expected stable code CHECK_FAILED, got %q", envelope.Error.Code)
	}
}

func TestBinary_FinishStrict_Exit1(t *testing.T) {
	dir := t.TempDir()
	if r := runBin(t, dir, "init"); r.code != 0 {
		t.Fatalf("init failed: exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	s := store.NewStore(dir)
	tl, err := s.ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks failed: %v", err)
	}
	tl.Tasks = append(tl.Tasks, model.Task{
		ID: "TASK-001", Title: "Blocked no reason", Status: model.StatusBlocked, Priority: model.PriorityMedium,
	})
	if err := s.WriteTasks(tl); err != nil {
		t.Fatalf("WriteTasks failed: %v", err)
	}

	r := runBin(t, dir, "finish", "--strict")
	if r.code != 1 {
		t.Errorf("expected exit 1 for finish --strict with warnings, got %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}

	// The finish sequence must have completed its cleanup: no project lock
	// left behind, and the session.finished event appended.
	if _, err := os.Stat(filepath.Join(dir, ".ctask", store.ProjectLockFile)); !os.IsNotExist(err) {
		t.Errorf("project lock left behind after finish: %v", err)
	}
	events, err := s.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if !hasCmdEvent(events, model.EventSessionFinished) {
		t.Errorf("expected session.finished event to be appended, got %v", events)
	}
}
