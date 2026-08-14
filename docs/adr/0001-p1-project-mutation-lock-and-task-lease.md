# ADR-0001: P1 - OS-Advisory Project Mutation Lock and Task Leases

- **Date:** 2026-08-13
- **Status:** Accepted (P1, uncommitted working tree at the time of writing)

## Context

P0 established the CLI process contract: a reentrant `NewRoot(deps)` command
tree, typed errors with stable machine codes, a single error renderer, and
stable process exit codes, all verified by real-binary subprocess tests.

Two coordination problems remained unsolved:

1. **Project mutation lock.** Multiple agents (processes) can run mutating
   `ctask` commands against the same `.ctask/` directory concurrently.
   The pre-P1 implementation used metadata-only locking (a `.ctask.lock`
   file with pid + expiry) which cannot distinguish a live holder from a
   crashed one and can be raced (two processes both read stale metadata and
   both proceed). We needed mutual exclusion that is enforced by the kernel,
   auto-released on process death, and cannot be stolen from a live holder.

2. **Task leases.** `claim` created a lock entry, but there was no
   per-acquisition identity, no recorded duration, no true renewal, and no
   strict ownership validation. `heartbeat` was a liveness stamp that did not
   actually extend the lease, and `release`/`done` could not prove ownership.
   Pre-P1 lock entries also had no migration path.

## Decision

### 1. Project mutation lock = OS advisory lock

- Every mutating command acquires a **real OS advisory lock** on
  `.ctask/.ctask.lock` using the pinned, mature, cross-platform
  `github.com/gofrs/flock v0.12.1` (`flock(2)` on Unix, `LockFileEx` on
  Windows).
- The lock file also carries JSON **metadata** (pid, command, agent, task,
  lease_id, created_at, expires_at). The metadata is informational for
  `ctask locks` and auditing; **the kernel flock is the source of truth** for
  mutual exclusion.
- A live holder is never stolen, regardless of recorded expiry. A conflicting
  acquire returns a typed `ProjectLockError` (mapped to `LOCK_CONFLICT`, exit
  3) and logs a `project.lock_conflict` event.
- Leftover metadata left by a crashed process (the OS released its lock
  automatically) is reclaimed on the next acquire and logged as
  `project.lock_stale_removed`. Corrupt/unreadable leftovers are reclaimed the
  same way.
- The lock **file is never unlinked** after release (unlink would create the
  classic race where two processes hold "the lock" on different inodes). It
  persists as an empty placeholder; an empty file means "no active lock" to
  every reader. `Release()` order: log `project.lock_released` first, then
  truncate metadata, then unlock + close.

### 2. Task leases

- `claim` creates a lease with:
  - a unique `lease_id` (128-bit cryptographically random, injectable via
    `lease.IDGen` for deterministic tests);
  - a recorded `lease_duration` (parsed from `--ttl`, validated fail-closed);
  - `expires_at = now + duration` and `heartbeat_at = acquired_at`.
- `heartbeat` is a **true renewal**: `expires_at` is extended by the full
  recorded `lease_duration` (now + duration), never merely stamped.
- Strict owner/lease validation for renew/release/done:
  - `--agent` must match the lease owner; `--lease-id`, when given, must match;
  - breaking a lease requires `--force` with an explicit `--reason`
    (`FORCE_REQUIRED` otherwise), and is audited via `task.lease_broken`;
  - an expired lease is stale and cleanable by anyone without force;
  - `done` on a leased task auto-releases the lease on success.
- Legacy (pre-P1) lock entries - missing `lease_id`/`lease_duration` or
  unparseable fields - are **fail-closed** (`LEGACY_STATE`): they block
  claims, cannot be renewed or released without `--force --reason`, and are
  surfaced as a warning by `ctask check`. This is the migration path: no
  silent upgrade of untrusted state.

### 3. Time and identity are injectable

`cmd.Dependencies` gained `Clock` (`internal/clock`) and `NewID`
(`internal/lease`) so all lease/lock expiry and identity logic is testable
deterministically (`clock.FixedClock`, `lease.StaticID`) with no sleeps and no
race-prone timing windows. The project lock accepts the same injection via
`store.ProjectLockOptions`.

### 4. Exit-code and machine-code extension

New typed machine codes were added to `internal/clierr`:
`LEASE_CONFLICT`, `LEASE_EXPIRED`, `FORCE_REQUIRED`, `LEASE_NOT_FOUND`,
`LEGACY_STATE`. Mapping (typed errors + `errors.Is` only, never strings):

- exit 3: `LOCK_CONFLICT`, `LEASE_CONFLICT`, `LEASE_EXPIRED` (contention);
- exit 2: `USAGE_ERROR`, `VALIDATION_ERROR`, `FORCE_REQUIRED`, invalid
  `--ttl` (caller/validation errors);
- exit 1: `LEGACY_STATE` and everything else (business/check failures).

The single renderer and single-JSON-document contract from P0 are preserved.

## Consequences

- **Lock file semantics changed:** the `.ctask.lock` file now persists as an
  empty placeholder after release. P0-era tests asserting file *absence* were
  updated to assert *no active lock* (`ReadProjectLock == nil`). Users and
  tooling must treat an empty `.ctask.lock` as "no active lock".
- **Metadata no longer blocks:** a leftover/expired lock file with no live
  flock holder is reclaimed, not a conflict. The only way to observe a
  conflict is a live holder (verified by a true multi-process binary test).
- **flock caveat:** advisory locks are not reliable on all network
  filesystems (e.g. some NFS setups). This is an accepted limitation for a
  local-first tool; documented for future work.
- **Schema:** `locks.yaml` entries gain optional `lease_id` and
  `lease_duration` fields. Old files remain readable and are treated as
  legacy/fail-closed. No other `.ctask` file formats changed.
- **Deterministic tests:** lease/lock unit and CLI tests no longer depend on
  wall-clock timing; renewal, expiry, and conflict behaviour are proven with
  exact timestamps.
- **Scope boundaries respected:** no MCP, no SQLite/memory adapters, no
  atomic-writer/journal, no path validation changes (those are later phases).
