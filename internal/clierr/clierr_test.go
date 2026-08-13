package clierr

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"usage", New(KindUsage, "missing argument"), 2},
		{"validation", New(KindValidation, "invalid priority"), 2},
		{"lock conflict", Wrap(KindLockConflict, errors.New("held"), "conflict"), 3},
		{"check failed", New(KindCheckFailed, "consistency check failed"), 1},
		{"not initialized", New(KindNotInitialized, "not initialized"), 1},
		{"not found", New(KindNotFound, "task not found"), 1},
		{"operation failed", New(KindOperation, "disk error"), 1},
		{"wrapped typed error stays classified", fmt.Errorf("outer: %w", Wrap(KindLockConflict, errors.New("held"), "conflict")), 3},
		{"unknown error falls back to 1", errors.New("boom"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestKindOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"typed kind", New(KindValidation, "x"), KindValidation},
		{"outermost typed kind wins", fmt.Errorf("outer: %w", New(KindNotFound, "x")), KindNotFound},
		{"typed error wrapping a typed error", Wrap(KindOperation, New(KindNotFound, "inner"), "outer"), KindOperation},
		{"unknown error is INTERNAL_ERROR", errors.New("boom"), KindInternal},
		{"nil is INTERNAL_ERROR fallback", nil, KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KindOf(tt.err); got != tt.want {
				t.Errorf("KindOf(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestErrorUnwrapPreservesCause(t *testing.T) {
	cause := errors.New("underlying cause")
	e := Wrap(KindOperation, cause, "op failed")

	if !errors.Is(e, cause) {
		t.Error("expected errors.Is to find the wrapped cause")
	}
	var typed *Error
	if !errors.As(e, &typed) {
		t.Fatal("expected errors.As to find *Error")
	}
	if typed.Kind != KindOperation {
		t.Errorf("expected KindOperation, got %q", typed.Kind)
	}
	if got := e.Error(); got != "op failed: underlying cause" {
		t.Errorf("unexpected Error() text: %q", got)
	}
}

func TestWrapEmptyPrefixRendersCauseOnly(t *testing.T) {
	e := Wrap(KindOperation, errors.New("plain message"), "")
	if got := e.Error(); got != "plain message" {
		t.Errorf("expected cause-only message, got %q", got)
	}
}
