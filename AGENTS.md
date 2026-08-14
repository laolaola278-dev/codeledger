# Agent Instructions for CodeLedger

This document is for AI coding agents. Follow these instructions when working on a project that uses CodeLedger.

## Before Starting Work

1. **Read the context.** Run `ctask context` or read `.ctask/context.md` to understand the current state of the project.
2. **Understand the goal.** Check the project goal in `.ctask/project.yaml` or the context output.
3. **Pick a task.** Use `ctask status` to see what needs to be done. Look at the "Next Suggested" task.
4. **Start the task.** Run `ctask claim <TASK-ID> --agent <name>` (or `ctask start <TASK-ID>` without locking) before you begin writing code.

## During Work

1. **Record findings.** If you discover something important (architecture insight, design decision, issue), record it with `ctask note <TASK-ID> "your note"`.
2. **Record decisions.** If you make an architectural decision, append it to `.ctask/decisions.md` with date, context, decision, and consequences.
3. **Stay focused.** Work on one task at a time. Do not start a new task before completing or blocking the current one.

## After Completing Code Changes

1. **Run tests.** Execute the relevant tests for your changes.
2. **Mark the task done.** Use `ctask done <TASK-ID>` with the following flags:
   - `--files` - Comma-separated list of files you modified
   - `--test` - The test command you ran
   - `--result` - The test result (passed, failed, skipped, unknown)
   - `--note` - A brief summary of what you did
   - `--auto-files` - Automatically detect changed files from Git and merge with `--files`
   - `--capture-diff` - Capture the full Git diff into `.ctask/evidence/<TASK-ID>.diff`

   By default `ctask done` does NOT auto-detect Git changed files; pass `--auto-files` to read them. Use `--capture-diff` to save a separate `.diff` evidence file. The Markdown evidence file (`.ctask/evidence/<TASK-ID>.md`) records task metadata, files, test result, and a diffstat summary (never the full diff). View evidence with `ctask evidence <TASK-ID>`.

3. **Example:**
   ```
   ctask done TASK-001 --files "auth.go,auth_test.go" --auto-files --capture-diff --test "go test ./..." --result passed --note "Implemented login flow with JWT tokens"
   ```

## If You Are Blocked

1. **Explain the blocker.** Use `ctask block <TASK-ID> "reason for blocking"`.
2. **Suggest a path forward.** Include what is needed to unblock in the reason.

## Rules

- **Do not** delete or modify the `.ctask/` directory structure.
- **Do not** falsify test results. If tests were not run, use `--result unknown`.
- **Do not** make large changes without updating task status.
- **Do not** end a session without running `ctask check` and `ctask finish` (or `ctask status` + `ctask context` + `ctask report`).
- **Do** keep decisions.md up to date with architectural decisions.
- **Do** update task status before switching to a different task.
- **Do** record all modified files when completing a task.

## Task Leases and Project Lock (P1)

CodeLedger serializes concurrent mutations with a real OS advisory project lock (`.ctask/.ctask.lock`, via `gofrs/flock`), and tracks task ownership with leases:

- `ctask claim <TASK-ID> --agent <name>` creates a lease with a unique `lease_id` and a TTL (default `120m`, `--ttl` to override). The task is set to `in_progress`.
- Keep a lease alive while working: `ctask heartbeat <TASK-ID> --agent <name> --lease-id <id>` renews it by the **full lease duration** (a true renewal).
- When finished, release it: `ctask release <TASK-ID> --agent <name> --lease-id <id>`. `ctask done` also releases the lease automatically when you pass `--agent` + `--lease-id`.
- Once a lease exists, `--agent` AND `--lease-id` are both **required** fencing credentials. To break another agent's lease, pass `--force` with an explicit `--reason` and a non-empty `--agent` actor (audited via a `task.lease_broken` event).
- **Expired** leases are fail-closed (`LEASE_EXPIRED`, exit 3): ordinary commands (including release) never clean them; re-claim the same task instead. Unrelated commands never sweep another task's expired entry.
- **Legacy locks** (pre-lease format, no `lease_id`) are **fail-closed** (`LEGACY_LEASE_REQUIRES_TAKEOVER`, exit 3): they block claims and require `--force --reason --agent` to take over. Run `ctask locks` to inspect, and `ctask check` surfaces legacy locks as warnings.

Concurrency contract: every mutating command (`add`, `claim`, `release`, `heartbeat`, `done`, `block`, `note`, `start`, `finish`, `evidence add`, `plan save`) holds the project lock for the duration of the mutation; a conflicting process fails fast with exit code 3 and a `LOCK_CONFLICT` message. Read-only commands (`status`, `next`, `locks`, `check`, `context`, `report`, ...) never take the project lock.

## Why This Matters

Following these rules ensures that:

- You can resume work correctly after a context compression or session restart.
- Other agents (or the same agent in a new session) can pick up where you left off.
- Humans can review your progress through reports and status.
- The project has an auditable trail of what was done and why.
- Architectural decisions are not lost when context windows fill up.

## Quick Reference

```bash
# Before work
ctask context        # Read current state
ctask status         # See what to do next
ctask claim TASK-001 --agent codex # Lock and begin working (prints the lease-id)

# During work
ctask note TASK-001 "found something important" --agent codex --lease-id <id>
ctask heartbeat TASK-001 --agent codex --lease-id <id>   # renew the lease

# After work
ctask release TASK-001 --agent codex --lease-id <id>   # release the lease (done also releases it)
ctask changed                 # List changed files
ctask diff --stat              # Diffstat summary
ctask done TASK-001 --files "..." --auto-files --capture-diff --test "..." --result passed --note "..."
ctask evidence add TASK-001 --type manual --content "..."
ctask evidence list TASK-001   # List evidence paths
ctask evidence show TASK-001   # Show Markdown evidence
ctask evidence TASK-001       # Alias for show

# When blocked
ctask block TASK-001 "waiting for API spec"

# Check consistency
ctask check

# End of session
ctask finish --agent codex --task TASK-001 --result passed --note "summary"
ctask status
ctask context
ctask report
```




