# CodeLedger

CodeLedger is a **local-first task boundary and evidence ledger for AI coding agent teams**.

It gives Codex, Claude Code, Cursor, Gemini CLI, OpenCode, Aider, and other coding agents a shared project state layer: tasks, claims, locks, Git evidence, consistency checks, session finish reports, and handoff context.

CodeLedger does not run agents.  
CodeLedger does not orchestrate agents.  
CodeLedger does not call AI APIs.  
CodeLedger is a CLI and file-based project ledger that helps agents and humans coordinate safely.

Modern coding agents are powerful, but they forget. Context windows are limited, sessions are ephemeral, and large projects require persistent task state.

CodeLedger gives every project a small `.ctask/` directory where agents and humans can track:

- Project goals
- Task breakdown and status
- Dependencies between tasks
- Key decisions
- Modified files
- Test results
- Context summaries
- Progress reports

## Why CodeLedger?

When you use AI coding agents (Codex, Claude Code, Cursor, Kimi, Gemini CLI, etc.) on a real project, you quickly run into these problems:

- The agent forgets what it did in previous sessions.
- Context compression drops important decisions.
- Large tasks get broken down but nobody tracks the pieces.
- The agent modifies files but nobody audits what changed.
- You want to see progress but have to scroll through chat logs or Git diffs.

CodeLedger solves this with a simple file-based protocol that any agent can read and write.

## What CodeLedger is NOT

- **Not a workflow engine.** It does not schedule, execute, or orchestrate tasks. Use Airflow, Temporal, or Dagster for that.
- **Not a Jira/Linear replacement.** Those are for humans. CodeLedger is for agents.
- **Not an AI agent.** It does not generate code, run tests, or make decisions.
- **Not a SaaS.** It is a local CLI tool. No accounts, no servers, no cloud.

## Quick Start

```bash
# Build from source
git clone https://cnb.cool/ai_agent-2026/codeledger.git
cd codeledger
go build -o ctask

# Initialize in your project
cd your-project
ctask init

# Add tasks
ctask add "Implement user login" --priority high
ctask add "Add login tests" --depends TASK-001

# Start working on a task
ctask claim TASK-001 --agent codex

# Mark as done with details
ctask done TASK-001 --files "auth.go,auth_test.go" --auto-files --capture-diff --test "go test ./..." --result passed --note "Implemented login flow"

# Run consistency check
ctask check

# End a session
ctask finish --task TASK-001 --agent codex --result passed --note "done"

# Check status
ctask status

# Generate context for your AI agent
ctask context

# Generate a progress report
ctask report
```

## Commands

| Command | Description |
|---------|-------------|
| `ctask init` | Initialize `.ctask/` directory in the current project |
| `ctask add <title>` | Add a new task (--priority, --depends) |
| `ctask list` | List all tasks (filter with `--status`) |
| `ctask next` | Show the next suggested task to work on |
| `ctask claim <task-id>` | Lock a task and mark as in_progress (--agent, --role, --ttl) |
| `ctask release <task-id>` | Release a locked task back to pending |
| `ctask heartbeat <task-id>` | Refresh the TTL on a claimed task lock |
| `ctask start <task-id>` | Mark a task as in progress (no lock) |
| `ctask done <task-id>` | Mark a task as completed (--files, --auto-files, --capture-diff, --test, --result, --note) |
| `ctask block <task-id> <reason>` | Mark a task as blocked |
| `ctask note <task-id> <note>` | Add a note to a task |
| `ctask changed` | List files changed in the Git working tree |
| `ctask diff` | Show Git diff (--stat for summary) |
| `ctask evidence add <task-id>` | Append an evidence entry to a task |
| `ctask evidence list <task-id>` | List evidence paths for a task |
| `ctask evidence <task-id>` | Show evidence recorded for a task |
| `ctask check` | Run consistency check on .ctask project state (--json, --strict, --verbose) |
| `ctask finish` | End an agent session: check, complete task, context, report, next (--json, --strict, --skip-context, --skip-report) |
| `ctask status` | Show project status overview |
| `ctask context` | Generate context summary for AI agents |
| `ctask report` | Generate a progress report |
| `ctask plan generate` | Print a structured AI planning prompt to stdout (--mode planning\|triage\|blocked, --agent) |
| `ctask plan save [PLAN-XXX]` | Parse an agent's plan text and persist it to `.ctask/plans/` (--input, --file) |
| `ctask plan show <plan-id>` | Show a saved plan (--prompt to include the original prompt) |
| `ctask plan list` | List all saved plans (--json) |

## `.ctask/` Directory Structure

```
.ctask/
  project.yaml      # Project metadata and agent policy
  tasks.yaml        # Task list with status, dependencies, files, tests
  decisions.md      # Architectural and design decisions
  context.md        # Generated context summary for AI agents
  events.jsonl      # Immutable event log for auditing
  evidence/         # Markdown evidence files for completed tasks
  reports/          # Generated progress reports
  plans/            # AI-assisted plans (PLAN-001.yaml, ...)
```

All files are plain text (YAML, JSONL, Markdown). They are human-readable, agent-friendly, and Git-friendly.

## AI-Assisted Planning (Phase 6)

CodeLedger does not reason: it only assembles the current `.ctask/` state into a structured prompt that you hand to your own AI agent. The agent's own model does the reasoning, and the result is saved back as an auditable plan.

```bash
# 1. Get a structured prompt (plain text, no LLM API calls)
ctask plan generate --mode planning --agent codex

# 2. Run the prompt through your agent's own model, then save the result
ctask plan save PLAN-001 --input "TASK-003: start | highest priority unblocked task"

# 3. Inspect history
ctask plan show PLAN-001
ctask plan list
```

Three prompt modes cover three scenarios:

| Mode | Scenario |
|------|----------|
| `planning` | Long session: what should I do next? |
| `triage` | Short session: what can I do right now? |
| `blocked` | Blocked: how do I unblock? |

Plans are stored as plain YAML in `.ctask/plans/` and each save appends a `plan.saved` event to `events.jsonl` for auditing. No LLM API is ever called, no model is bundled, and plans are never auto-executed.

## Using with AI Coding Agents

See [AGENTS.md](AGENTS.md) for instructions on how AI coding agents should use CodeLedger.

In short:

1. Start each session by reading `ctask context`.
2. Use `ctask claim` or `ctask start` before beginning a task.
3. Use `ctask done` with `--files`, `--test`, `--result` after completing a task. When run inside a Git repository, `ctask done` also records changed files, diff stat, and diff output under `.ctask/evidence/`.
4. Run `ctask check` to verify project consistency before finishing.
5. Use `ctask block` when stuck.
6. End each session with `ctask finish` (or `ctask status` + `ctask context` + `ctask report`).

## Verification

Before submitting changes, run the verification commands documented in [docs/verification.md](docs/verification.md):

```bash
go fmt ./...
go build ./...
go vet ./...
go test -count=1 ./...
```

## MCP Status

MCP support is planned for a later phase.

There is experimental MCP code in local development, but it is intentionally excluded from the current mainline release until the CLI task ledger, evidence workflow, check, and finish commands are stable and documented.

Current release scope:
- CLI task ledger
- task claim / lock
- Git evidence capture
- consistency check
- session finish

Not included in this release:
- MCP server
- browser extension
- local bridge
- hosted service

## Roadmap

- **MVP (done):** `init`, `add`, `list`, `start`, `done`, `block`, `note`, `status`, `context`, `report`.
- **Phase 1 (done):** `next`, `claim`, `release`, `heartbeat`, `done` with lock release, `.ctask/locks.yaml`.
- **Phase 2.1 (done):** `changed`, `diff`, `evidence` add/list/show, `done --auto-files`, `done --capture-diff`, Git evidence capture.
- **Phase 3 (done):** `check` (26 rules, --json, --strict, --verbose), `finish` (5-step session end sequence).
- **Phase 4 (planned):** Editor integrations (VS Code, Cursor rules, Claude Code commands).
- **Phase 5 (planned):** MCP server for direct agent tool access.
- **Phase 6 (done):** AI-assisted planning (`ctask plan generate/save/show/list`, `.ctask/plans/`, no LLM API).

## License

MIT
