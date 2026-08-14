package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/model"
	"github.com/codeledger/codeledger/internal/store"
)

// releaseAuditFails returns an audit sink that passes every lock lifecycle
// event through to the store except the release event, so acquisition
// succeeds normally and only the release audit fails.
func releaseAuditFails(s *store.Store) func(model.Event) error {
	return func(evt model.Event) error {
		if evt.Type == model.EventProjectLockReleased {
			return errors.New("release audit down")
		}
		return s.AppendEvent(evt)
	}
}

// TestWithProjectLock_CallbackSuccessReleaseFailure: a callback success with a
// failing release audit must return a non-nil error (never exit 0), and the
// next wrapper in the same process must succeed once the sink is fixed.
func TestWithProjectLock_CallbackSuccessReleaseFailure(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	env.LockAudit = releaseAuditFails(env.store())

	called := false
	err := withProjectLock(env.deps(), env.store(), "claim", "", "", func() error {
		called = true
		return nil
	})
	if !called {
		t.Fatal("callback should have run")
	}
	if err == nil {
		t.Fatal("expected a release error when the release audit fails")
	}
	if !strings.Contains(err.Error(), "release audit down") {
		t.Errorf("expected release audit failure in error, got: %v", err)
	}

	env.LockAudit = nil
	if err := withProjectLock(env.deps(), env.store(), "claim", "", "", func() error { return nil }); err != nil {
		t.Fatalf("next wrapper after fixing audit should succeed, got: %v", err)
	}
}

// TestWithProjectLock_CallbackAndReleaseFailure: when both the callback and
// the release fail, the joined error must preserve the callback's typed
// classification (exit code) while still exposing the release failure.
func TestWithProjectLock_CallbackAndReleaseFailure(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	env.LockAudit = releaseAuditFails(env.store())

	mainErr := clierr.New(clierr.KindLeaseConflict, "lease conflict main error")
	err := withProjectLock(env.deps(), env.store(), "claim", "", "", func() error { return mainErr })
	if err == nil {
		t.Fatal("expected a combined error")
	}
	if got := clierr.KindOf(err); got != clierr.KindLeaseConflict {
		t.Errorf("expected main kind LEASE_CONFLICT, got %q", got)
	}
	if got := clierr.ExitCode(err); got != clierr.ExitContention {
		t.Errorf("expected exit code 3, got %d", got)
	}
	if !strings.Contains(err.Error(), "release audit down") {
		t.Errorf("expected release audit failure in joined error, got: %v", err)
	}
}

// TestWithProjectLock_NoLeakAfterAcquireFailure: an acquired-event failure
// must return an error without running the callback, and the next mutation in
// the same process must succeed once the sink is fixed.
func TestWithProjectLock_NoLeakAfterAcquireFailure(t *testing.T) {
	env := newTestEnv(t)
	env.initProject()
	env.LockAudit = func(model.Event) error { return errors.New("acquire audit down") }

	called := false
	err := withProjectLock(env.deps(), env.store(), "add", "", "", func() error {
		called = true
		return nil
	})
	if called {
		t.Fatal("callback must not run when acquisition fails")
	}
	if err == nil {
		t.Fatal("expected an acquire error")
	}
	if !strings.Contains(err.Error(), "acquire audit down") {
		t.Errorf("expected acquire audit failure in error, got: %v", err)
	}

	env.LockAudit = nil
	if err := withProjectLock(env.deps(), env.store(), "add", "", "", func() error { return nil }); err != nil {
		t.Fatalf("next wrapper after fixing audit should succeed, got: %v", err)
	}
}
