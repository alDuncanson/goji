# goji Architecture

`goji` is an unattended issue-orchestration service. It polls an issue tracker (GitHub today), selects eligible work, prepares per-issue workspaces, launches an external coding-agent CLI, and continuously reconciles runtime state.

## Goals

1. Keep orchestration deterministic and stateful.
2. Make runtime policy editable in-repo through `WORKFLOW.md`.
3. Support pluggable coding-agent CLIs without changing scheduler logic.
4. Provide both machine-readable logs and human-friendly live observability.

## High-Level Component Map

1. `cmd/goji/main.go`: Thin process entrypoint; delegates to app layer.
2. `internal/app`: CLI argument handling, logging setup, process wiring, signal lifecycle.
3. `internal/workflow`: Loads and hot-reloads `WORKFLOW.md` (front matter config + prompt template body).
4. `internal/config`: Converts untyped workflow config into typed runtime config with defaults/env resolution.
5. `internal/orchestrator`: Core scheduler/event loop; dispatch, retry, reconciliation, runtime snapshots.
6. `internal/tracker/github`: Tracker adapter implemented via `gh` CLI.
7. `internal/workspace`: Creates/removes issue workspaces and executes lifecycle hooks safely.
8. `internal/agent`: Runs coding-agent CLI turns and normalizes output events/token usage.
9. `internal/tui`: Bubble Tea dashboard that renders orchestrator snapshots.
10. `internal/model`: Tracker-neutral issue shape plus runtime event/update types.

## Runtime Boot Sequence

1. Process starts in `main`, then calls `app.Run(args)`.
2. `app.Run` routes commands (`run`, `--help`, `--version`).
3. `runCommand` validates workflow path, initializes JSON logger, and installs signal-aware context cancellation.
4. Workflow store is created (`workflow.NewStore`) and started (`Store.Start`) for hot-reload.
5. Orchestrator is built with concrete dependencies:
   - workflow store
   - GitHub tracker adapter
   - workspace manager
   - agent runner
6. Orchestrator starts and owns the long-running scheduler loop.
7. UI mode:
   - default: Bubble Tea TUI starts and polls snapshots
   - headless (`--no-tui`): process blocks on context cancellation

## Orchestration Loop Design

The orchestrator uses a single owner goroutine for mutable scheduler state and coordinates workers with channels.

### Internal Channels

1. `refreshCh`: Manual refresh requests (TUI key `r`).
2. `updateCh`: Streaming updates from active worker turns.
3. `doneCh`: Worker completion/failure notifications.
4. `snapshotCh`: Request/response channel for TUI snapshot reads.

### Loop Cadence

1. Heartbeat ticker runs every `250ms`.
2. On each beat:
   - process due retries
   - run a poll tick when `nextPollDueAt` is reached
3. Poll tick actions:
   - reload workflow/config (last-known-good fallback on parse error)
   - reconcile currently running workers against tracker state and stall timeout
   - fetch candidate issues from tracker
   - sort dispatch order (priority, then created time, then identifier)
   - dispatch while global and state-specific slots remain

## Dispatch And Worker Lifecycle

### Dispatch Gating

An issue is dispatchable only when all conditions pass:

1. Required fields exist (`id`, `identifier`, `title`, `state`).
2. State is in active set and not in terminal set.
3. `Todo` issues are not blocked by unresolved blockers.
4. Not already claimed/running.
5. Global and per-state concurrency limits allow it.

### Worker Execution Steps

1. Create/reuse workspace under `workspace.root` by sanitized issue key.
2. Run optional `hooks.after_create` on first creation.
3. Run optional `hooks.before_run`.
4. Execute up to `agent.max_turns` turns:
   - Turn 1 prompt uses Liquid template from workflow body.
   - Later turns use continuation prompt text.
   - Runner executes external CLI command in workspace.
   - Streaming updates are normalized into `model.AgentUpdate` events.
   - After each turn, tracker state is refreshed; loop stops if issue leaves active states.
5. Run optional `hooks.after_run` (non-fatal).
6. Notify orchestrator success/failure via `doneCh`.

### Completion, Retry, And Continuation

1. Successful worker exits are queued for fast continuation retry (`1s`) to continue active issues in subsequent turns.
2. Failures enter exponential backoff retry queue (base 10s, capped by `agent.max_retry_backoff_ms`).
3. Retry attempts re-check candidate eligibility before relaunch.
4. Running workers are proactively terminated when issue becomes terminal/non-active or when stall timeout is exceeded.

## Tracker Adapter (GitHub)

`internal/tracker/github` uses `gh` commands:

1. `gh issue list --state all --json ...` for candidate/state filtering.
2. `gh issue view <number> --json ...` for targeted refreshes.

Normalization rules:

1. State from first `state:` label when present.
2. Fallback state mapping: closed -> `Done`, otherwise `Todo`.
3. Priority from `priority:<n>` label.
4. Blockers from `blocked-by:` labels.
5. All labels are normalized to lowercase.

## Configuration And Policy Model

`WORKFLOW.md` has two layers:

1. YAML front matter: operational config.
2. Markdown body: Liquid prompt template.

`internal/config.Parse` applies defaults and supports:

1. Env indirection for values prefixed with `$`.
2. Path normalization/`~` expansion for workspace roots.
3. Backward-compatible `codex.*` runner config and preferred `runner.*` extension block.

Validation requires:

1. `tracker.kind` present and currently `github`.
2. non-empty `tracker.repo`.
3. non-empty runner command.

## Workspace Safety Model

`internal/workspace` enforces containment and lifecycle behavior:

1. Workspace key sanitation (`[^A-Za-z0-9._-]` -> `_`).
2. Root containment check via `filepath.Rel` guard against path escape.
3. Hook execution with bounded timeout.
4. Fatal hooks for `after_create`/`before_run`; non-fatal hooks for `after_run`/`before_remove`.

## Agent Runner Contract

Runner executes a shell command with contextual env vars such as:

1. `GOJI_ISSUE_*` metadata.
2. `GOJI_WORKSPACE` and `GOJI_PROMPT_FILE`.
3. `GOJI_TURN_NUMBER` and optional `GOJI_ATTEMPT`.

Output handling:

1. `plain` format: line-based `output` events.
2. JSON stream formats: best-effort extraction of event/message/thread/turn/session and token usage.
3. Stderr is surfaced as `stderr` runtime events.

## Observability Surfaces

1. Structured JSON logs to stderr and `log/goji.log`.
2. In-memory runtime event ring (max 100 recent events).
3. Snapshot API exposed by orchestrator for UI/automation.
4. Bubble Tea TUI showing:
   - poll cadence/state
   - running sessions and latest events
   - retry queue timing/errors
   - aggregate token/runtime counters

## Concurrency And State Ownership

1. Scheduler state is mutated only inside orchestrator loop goroutine.
2. Worker goroutines are isolated and communicate with orchestrator only via channels.
3. Workflow store uses its own lock-protected state for live config reload.

This keeps race-prone orchestration state centralized while preserving parallel worker execution.

## Extension Points

1. Tracker implementations can be added by implementing `internal/tracker.Client`.
2. Runner behavior is command-driven; different CLIs can be swapped via config/flag override.
3. Prompt and policy changes do not require recompilation; update `WORKFLOW.md` and hot-reload applies.

## Verified Operational Checks

The repository currently passes:

1. `make ci` (format check, `go vet`, `go test ./...`, and `go build ./cmd/goji`)
2. `go run ./cmd/goji --help`
3. `go run ./cmd/goji --version`
