---
name: running-goji-simulation
description: Runs a full GitHub-backed goji orchestration simulation from issue creation through PR merge and CI verification. Use when validating goji end-to-end behavior, proving label-gated dispatch, or running periodic orchestration health checks.
---

# Running Goji Simulation

Runs an end-to-end simulation against a dedicated GitHub repository and produces evidence artifacts.

## What This Skill Does

1. Ensures a simulation repo exists (`<owner>/goji-simulation`) and has baseline Go + CI files.
2. Ensures orchestration labels exist (`state:in-progress`, `state:done`, `priority:1`).
3. Creates a simulation issue that requires code changes, tests, branch + PR creation, and issue label transition.
4. Starts `goji` headless against a simulation workflow.
5. Verifies the issue is not dispatched before the active-state label is applied.
6. Applies the active-state label and waits for dispatch, PR creation, CI success, and merge.
7. Captures run outputs under `log/simulation/<run-id>/`.

## Run It

Run `scripts/run-e2e.sh` from the goji project root.

## Optional Environment Overrides

1. `GOJI_PROJECT_ROOT`: Path to the goji repo root (default: current directory).
2. `GOJI_SIM_REPO`: Simulation repo in `owner/name` format (default: `<gh-user>/goji-simulation`).
3. `GOJI_CMD`: Command prefix used to start goji (default: `go run ./cmd/goji run`).
4. `GOJI_SIM_RUN_SECONDS`: Max wait for long-running phases in seconds (default: `900`).
5. `GOJI_SIM_ARTIFACT_ROOT`: Artifact root directory (default: `<project>/log/simulation`).
6. `GOJI_SIM_TOKEN_PREFIX`: Token prefix for generated test cases (default: `simtoken`).

## Expected Artifacts

A successful run writes these files in `log/simulation/<run-id>/`:

1. `summary.md`: Human-readable links and pass/fail checkpoints.
2. `result.env`: Machine-readable run metadata.
3. `WORKFLOW.md`: Exact workflow used for that run.
4. `goji-logs/goji.log`: Structured goji runtime logs.
5. `goji-process.log`: Process stdout/stderr capture.

## Failure Triage

If a run fails:

1. Read `summary.md` first for failing step.
2. Inspect `goji-logs/goji.log` for `worker_failed`, `dispatch_validation_failed`, or tracker errors.
3. Inspect the created issue and PR for missing label transition, branch push, or PR formatting problems.
4. Re-run `scripts/run-e2e.sh` after fixing root cause; each run uses a fresh tokenized issue.
