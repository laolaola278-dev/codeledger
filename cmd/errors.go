package cmd

import (
	"errors"

	"github.com/codeledger/codeledger/internal/clierr"
	"github.com/codeledger/codeledger/internal/service"
	"github.com/codeledger/codeledger/internal/store"
	"github.com/spf13/cobra"
)

// newStore returns a Store rooted at the dependency working directory.
// An empty WorkDir means the process current directory.
func newStore(deps Dependencies) *store.Store {
	wd := deps.WorkDir
	if wd == "" {
		wd = "."
	}
	return store.NewStore(wd)
}

// requireInit returns a NOT_INITIALIZED typed error when the project has not
// been initialized, so the process exits with code 1.
func requireInit(s *store.Store) error {
	if err := s.RequireInit(); err != nil {
		return clierr.Wrap(clierr.KindNotInitialized, err, "project not initialized")
	}
	return nil
}

// classifyErr wraps a service/store error with a stable machine kind.
// Task-not-found errors become NOT_FOUND; pre-typed errors pass through
// unchanged so their kind (e.g. LOCK_CONFLICT) is never masked.
func classifyErr(prefix string, err error) error {
	if err == nil {
		return nil
	}
	var ce *clierr.Error
	if errors.As(err, &ce) {
		return err
	}
	if errors.Is(err, service.ErrTaskNotFound) {
		return clierr.Wrap(clierr.KindNotFound, err, "%s", prefix)
	}
	return clierr.Wrap(clierr.KindOperation, err, "%s", prefix)
}

// noArgs returns a positional-args validator that rejects any argument with
// a typed USAGE_ERROR (exit code 2), mirroring cobra.NoArgs.
func noArgs() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return clierr.New(clierr.KindUsage, "unknown command %q for %q", args[0], cmd.CommandPath())
		}
		return nil
	}
}

// exactArgs returns a positional-args validator requiring exactly n
// arguments, with a typed USAGE_ERROR otherwise (exit code 2).
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return clierr.New(clierr.KindUsage, "accepts %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

// maxNArgs returns a positional-args validator requiring at most n
// arguments, with a typed USAGE_ERROR otherwise (exit code 2).
func maxNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return clierr.New(clierr.KindUsage, "accepts at most %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}
