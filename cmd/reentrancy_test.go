package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// TestParallelRootsIndependent constructs and executes 20 fresh command trees
// in parallel, each with its own temp dir and buffers. A high-priority task
// followed by an unset-priority task must not leak flag values across
// executions, and no worker may observe another worker's data.
func TestParallelRootsIndependent(t *testing.T) {
	const n = 20
	type result struct {
		errText string
		out     string
	}
	results := make([]result, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir, err := os.MkdirTemp("", "ctask-reentrant-*")
			if err != nil {
				results[i].errText = "MkdirTemp: " + err.Error()
				return
			}
			defer os.RemoveAll(dir)

			out := &bytes.Buffer{}
			errb := &bytes.Buffer{}
			deps := Dependencies{Stdin: strings.NewReader(""), Stdout: out, Stderr: errb, WorkDir: dir}

			if err := runRoot(deps, "init"); err != nil {
				results[i].errText = "init: " + err.Error()
				return
			}
			// First add carries flags; the second must not inherit them.
			if err := runRoot(deps, "add", fmt.Sprintf("task-%d", i), "--priority", "high", "--description", fmt.Sprintf("desc-%d", i)); err != nil {
				results[i].errText = "add: " + err.Error()
				return
			}
			if err := runRoot(deps, "add", fmt.Sprintf("plain-%d", i)); err != nil {
				results[i].errText = "add plain: " + err.Error()
				return
			}
			if err := runRoot(deps, "list"); err != nil {
				results[i].errText = "list: " + err.Error()
				return
			}
			results[i].out = out.String()

			// Verify the plain task really got the default priority.
			tl, err := store.NewStore(dir).ReadTasks()
			if err != nil {
				results[i].errText = "ReadTasks: " + err.Error()
				return
			}
			for _, task := range tl.Tasks {
				if strings.HasPrefix(task.ID, "plain") && task.Priority != model.PriorityMedium {
					results[i].errText = fmt.Sprintf("flag leaked: %s priority = %q", task.ID, task.Priority)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r.errText != "" {
			t.Errorf("worker %d: %s", i, r.errText)
			continue
		}
		if !strings.Contains(r.out, fmt.Sprintf("task-%d", i)) {
			t.Errorf("worker %d: output missing its own task: %q", i, r.out)
		}
		if strings.Contains(r.out, fmt.Sprintf("task-%d", (i+1)%n)) {
			t.Errorf("worker %d: output leaked another worker's task: %q", i, r.out)
		}
	}
}

// TestSequentialExecutionsNoFlagLeak runs several executions against the same
// environment; each builds a fresh root, so no flag value from a previous run
// can persist.
func TestSequentialExecutionsNoFlagLeak(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()

	if _, err := env.run("add", "high-task", "--priority", "high"); err != nil {
		t.Fatalf("add high failed: %v", err)
	}
	if _, err := env.run("add", "plain-task"); err != nil {
		t.Fatalf("add plain failed: %v", err)
	}
	if _, err := env.run("add", "low-task", "--priority", "low"); err != nil {
		t.Fatalf("add low failed: %v", err)
	}
	if _, err := env.run("add", "plain-task-2"); err != nil {
		t.Fatalf("add plain 2 failed: %v", err)
	}

	tl, err := env.store().ReadTasks()
	if err != nil {
		t.Fatalf("ReadTasks failed: %v", err)
	}
	for _, task := range tl.Tasks {
		want := model.PriorityMedium
		switch {
		case strings.HasPrefix(task.Title, "high"):
			want = model.PriorityHigh
		case strings.HasPrefix(task.Title, "low"):
			want = model.PriorityLow
		}
		if task.Priority != want {
			t.Errorf("task %s (%s): priority = %q, want %q", task.ID, task.Title, task.Priority, want)
		}
	}
}

// runRoot builds a fresh root for deps and runs args, returning the error.
func runRoot(deps Dependencies, args ...string) error {
	root := NewRoot(deps)
	root.SetArgs(args)
	return root.Execute()
}
