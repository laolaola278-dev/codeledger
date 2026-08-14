package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codeledger/codeledger/internal/clock"
	"github.com/codeledger/codeledger/internal/lease"
)

// leaseEnv returns an environment with a deterministic clock and lease ID so
// lease behaviour is tested without sleeping. The clock starts at t0 and can
// be advanced by mutating env.Clock before the next execution.
func leaseEnv(t *testing.T, t0 time.Time) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.initProject()
	env.Clock = clock.FixedClock{T: t0}
	env.NewID = lease.StaticID("lease-cli-0001")
	return env
}

// cliClaim claims taskID for agent with a 30m lease using the real Execute
// path, returning the exit code.
func cliClaim(env *testEnv, taskID, agent string) int {
	return Execute(context.Background(), env.deps(), []string{"claim", taskID, "--agent", agent, "--ttl", "30m"})
}

func TestLeaseCLI_ClaimHeartbeatReleaseFlow(t *testing.T) {
	env := leaseEnv(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if _, err := env.run("add", "Flow task"); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	if code := cliClaim(env, "TASK-001", "agent1"); code != 0 {
		t.Fatalf("claim failed with exit %d\nstderr: %s", code, env.Err.String())
	}
	if !strings.Contains(env.Out.String(), "Lease ID: lease-cli-0001") {
		t.Errorf("expected injected lease id in claim output, got %q", env.Out.String())
	}

	// Advance 10 minutes, then heartbeat: the expiry must move forward by the
	// full 30m (true renewal). The exact lease-id is required.
	env.Clock = clock.FixedClock{T: time.Date(2026, 1, 2, 3, 14, 5, 0, time.UTC)}
	code := Execute(context.Background(), env.deps(), []string{"heartbeat", "TASK-001", "--agent", "agent1", "--lease-id", "lease-cli-0001"})
	if code != 0 {
		t.Fatalf("heartbeat failed with exit %d\nstderr: %s", code, env.Err.String())
	}
	if !strings.Contains(env.Out.String(), "renewed") {
		t.Errorf("expected renewal output, got %q", env.Out.String())
	}

	// The lease is still valid 35 minutes after the original acquisition.
	env.Clock = clock.FixedClock{T: time.Date(2026, 1, 2, 3, 39, 5, 0, time.UTC)}
	if code := Execute(context.Background(), env.deps(), []string{"heartbeat", "TASK-001", "--agent", "agent1", "--lease-id", "lease-cli-0001"}); code != 0 {
		t.Errorf("expected lease still valid after renewal, exit %d\nstderr: %s", code, env.Err.String())
	}

	// Release by the owner (exact agent + lease-id).
	if code := Execute(context.Background(), env.deps(), []string{"release", "TASK-001", "--agent", "agent1", "--lease-id", "lease-cli-0001"}); code != 0 {
		t.Fatalf("release failed with exit %d\nstderr: %s", code, env.Err.String())
	}
}

// TestExecute_LeaseExitCodes drives the stable exit-code contract for the P1
// lease errors through the real Execute boundary (typed errors only - the
// contract is never derived from error strings).
func TestExecute_LeaseExitCodes(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	t.Run("claim conflict", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		if code := cliClaim(env, "TASK-001", "agent1"); code != 0 {
			t.Fatalf("first claim failed: %d", code)
		}
		code := cliClaim(env, "TASK-001", "agent2")
		if code != 3 {
			t.Errorf("expected exit 3 for lease conflict, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("release force without reason", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		code := Execute(context.Background(), env.deps(), []string{"release", "TASK-001", "--agent", "agent2", "--force"})
		if code != 2 {
			t.Errorf("expected exit 2 for --force without --reason, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("release force without actor", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		code := Execute(context.Background(), env.deps(), []string{"release", "TASK-001", "--force", "--reason", "cleanup"})
		if code != 2 {
			t.Errorf("expected exit 2 for --force without --agent, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("release non-owner without force", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		// Missing lease-id on an active lease: LEASE_REQUIRED (exit 3).
		code := Execute(context.Background(), env.deps(), []string{"release", "TASK-001", "--agent", "agent2"})
		if code != 3 {
			t.Errorf("expected exit 3 for missing lease-id release, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("heartbeat missing lease-id", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		code := Execute(context.Background(), env.deps(), []string{"heartbeat", "TASK-001", "--agent", "agent1"})
		if code != 3 {
			t.Errorf("expected exit 3 for missing lease-id heartbeat, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("heartbeat wrong agent", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		code := Execute(context.Background(), env.deps(), []string{"heartbeat", "TASK-001", "--agent", "agent2", "--lease-id", "lease-cli-0001"})
		if code != 3 {
			t.Errorf("expected exit 3 for heartbeat by non-owner, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("heartbeat expired lease", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		env.Clock = clock.FixedClock{T: t0.Add(31 * time.Minute)}
		code := Execute(context.Background(), env.deps(), []string{"heartbeat", "TASK-001", "--agent", "agent1", "--lease-id", "lease-cli-0001"})
		if code != 3 {
			t.Errorf("expected exit 3 for expired lease, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("done without credentials", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		code := Execute(context.Background(), env.deps(), []string{"done", "TASK-001", "--result", "passed"})
		if code != 3 {
			t.Errorf("expected exit 3 for done without credentials, got %d\nstderr: %s", code, env.Err.String())
		}
		// With the exact owner it succeeds.
		code = Execute(context.Background(), env.deps(), []string{"done", "TASK-001", "--result", "passed", "--agent", "agent1", "--lease-id", "lease-cli-0001"})
		if code != 0 {
			t.Errorf("expected exit 0 for owner done, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("claim over legacy lock fail-closed exit 3", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		writeLegacyLock(t, env, "TASK-001", "old-agent")
		code := cliClaim(env, "TASK-001", "agent2")
		if code != 3 {
			t.Errorf("expected exit 3 for legacy claim, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("release legacy lock requires force takeover exit 3", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		writeLegacyLock(t, env, "TASK-001", "old-agent")
		code := Execute(context.Background(), env.deps(), []string{"release", "TASK-001", "--agent", "old-agent"})
		if code != 3 {
			t.Errorf("expected exit 3 for legacy release without force, got %d\nstderr: %s", code, env.Err.String())
		}
		code = Execute(context.Background(), env.deps(), []string{"release", "TASK-001", "--agent", "old-agent", "--force", "--reason", "migrate"})
		if code != 0 {
			t.Errorf("expected exit 0 for legacy release with force+reason+agent, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("invalid ttl is validation", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		code := Execute(context.Background(), env.deps(), []string{"claim", "TASK-001", "--agent", "agent1", "--ttl", "bogus"})
		if code != 2 {
			t.Errorf("expected exit 2 for invalid ttl, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("claim without agent is usage", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		code := Execute(context.Background(), env.deps(), []string{"claim", "TASK-001"})
		if code != 2 {
			t.Errorf("expected exit 2 for missing --agent, got %d\nstderr: %s", code, env.Err.String())
		}
	})

	t.Run("release force with reason and actor breaks lease", func(t *testing.T) {
		env := leaseEnv(t, t0)
		if _, err := env.run("add", "Task"); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		cliClaim(env, "TASK-001", "agent1")
		code := Execute(context.Background(), env.deps(), []string{"release", "TASK-001", "--agent", "agent2", "--force", "--reason", "cleanup"})
		if code != 0 {
			t.Errorf("expected exit 0 for forced release, got %d\nstderr: %s", code, env.Err.String())
		}
	})
}

// TestExecute_LeaseErrorKinds verifies the typed error classification at the
// command layer (never derived from strings).
func TestExecute_LeaseErrorKinds(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	env := leaseEnv(t, t0)
	if _, err := env.run("add", "Task"); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	cliClaim(env, "TASK-001", "agent1")

	_, err := env.run("heartbeat", "TASK-001", "--agent", "agent2", "--lease-id", "lease-cli-0001")
	if err == nil {
		t.Fatal("expected heartbeat error")
	}
	if got := kindOf(t, err); got != "LEASE_MISMATCH" {
		t.Errorf("expected LEASE_MISMATCH kind, got %q", got)
	}
}
