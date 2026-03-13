# goji

`goji` is a Go implementation of Symphony-style orchestration: it polls an issue tracker, claims eligible work, prepares per-issue workspaces, and launches coding-agent CLI sessions to execute ticket work autonomously.

This implementation follows [OpenAI Symphony SPEC v1](../symphony/SPEC.md) semantics where possible, with these deliberate substitutions:

1. `tracker.kind: github` using the `gh` CLI.
2. Amp CLI as the default coding agent runner.
3. A pluggable runner contract so users can swap in Codex, Claude Code, Gemini CLI, or other CLIs.
4. Bubble Tea TUI observability built into `goji run`.

## Why goji

1. Stateful orchestrator loop with dispatch/retry/reconciliation semantics.
2. Deterministic per-issue workspaces with safety checks and lifecycle hooks.
3. Workflow policy in-repo via `WORKFLOW.md` with dynamic reload.
4. Structured logs plus real-time terminal observability.
5. Harness-engineering-first operating model.

## Architecture

1. `internal/workflow`: loads and hot-reloads `WORKFLOW.md`.
2. `internal/config`: typed config with defaults, env indirection, and validation.
3. `internal/tracker/github`: GitHub issue adapter powered by `gh`.
4. `internal/workspace`: workspace creation, hook execution, root-containment safety.
5. `internal/agent`: pluggable CLI runner (Amp default) with JSONL event parsing.
6. `internal/orchestrator`: poll loop, claims, retries, reconciliation, snapshots.
7. `internal/tui`: Bubble Tea dashboard for runtime observability.

## Prerequisites

1. Go `1.25+`.
2. `gh` CLI authenticated against the target repository.
3. A coding-agent CLI (Amp by default).
4. A repository configured with harness engineering practices.

## Quick Start

1. Copy [WORKFLOW.md](./WORKFLOW.md) into your target repository and customize it.
2. Set `GOJI_GITHUB_REPO=owner/repo` (or `tracker.repo` directly in workflow).
3. Run `go run ./cmd/goji run /path/to/WORKFLOW.md`.

Default command:

```bash
go run ./cmd/goji run ./WORKFLOW.md
```

Headless mode:

```bash
go run ./cmd/goji run --no-tui ./WORKFLOW.md
```

Override tracker repo and agent command at runtime:

```bash
go run ./cmd/goji run --repo owner/repo --agent-command "codex app-server" ./WORKFLOW.md
```

## GitHub State Mapping

`goji` derives issue orchestration state from labels by default.

1. Label prefix `state:` controls normalized state, e.g. `state:in progress`.
2. If no state label exists, `OPEN -> Todo`, `CLOSED -> Done`.
3. Priority label prefix `priority:` maps to integer priority, e.g. `priority:1`.
4. Blocker labels with prefix `blocked-by:` populate normalized blocker refs.

## Runner Configuration

`goji` supports two compatible config surfaces:

1. `codex.command` (spec-compatible shell command field).
2. `runner.*` extension (preferred for non-Codex CLIs).

Example `runner` config:

```yaml
runner:
  command: amp --execute --stream-json --mode deep
  prompt_mode: stdin
  output_format: amp_stream_json
  env:
    AMP_API_KEY: $AMP_API_KEY
```

If `runner.command` is omitted, `goji` falls back to `codex.command`, then to `amp --execute --stream-json`.

## Observability

The Bubble Tea dashboard shows:

1. Poll status and next refresh timing.
2. Running sessions with last event/message, token counters, turn counts.
3. Retry queue with due times and backoff reasons.
4. Aggregate token/runtime totals and recent runtime events.

Structured logs are written to stderr and `./log/goji.log` by default.

## Harness Engineering

Read [docs/harness-engineering.md](./docs/harness-engineering.md). The short version:

1. Make workspaces reproducible and self-bootstrapping via hooks.
2. Ensure deterministic validation commands exist and are CI-compatible.
3. Keep issue and PR automation pathways explicit inside your prompt policy.
4. Minimize ambient credentials and constrain command surfaces.

## Development

```bash
make ci
```

CI runs formatting, vet, tests, and build.

## Status

This is an initial public cut focused on orchestration correctness and extensibility. Additional protocol adapters and richer tracker semantics can be layered without changing the core scheduler contract.
