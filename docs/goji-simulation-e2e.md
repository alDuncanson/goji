# goji Simulation E2E

This project includes a periodic end-to-end simulation that validates `goji` orchestration against a real GitHub repository.

## Skill Entry Point

Use the skill at `.agents/skills/running-goji-simulation/`.

The executable harness is:

`./.agents/skills/running-goji-simulation/scripts/run-e2e.sh`

## What The Simulation Verifies

1. A simulation issue is created in `alDuncanson/goji-simulation`.
2. The issue is not dispatched until `state:in-progress` is applied.
3. `goji` creates an isolated workspace and clones the target repo.
4. The coding agent writes code and tests, runs `go test ./...`, and opens a PR with `Fixes #<issue>`.
5. PR CI check (`go test ./...`) runs and must pass.
6. PR is merged and the issue reaches closed state.
7. Run artifacts are emitted under `log/simulation/<run-id>/`.

## Latest Validated Run

1. Run ID: `20260313053648`
2. Issue: https://github.com/alDuncanson/goji-simulation/issues/11
3. PR: https://github.com/alDuncanson/goji-simulation/pull/12
4. Artifacts: `log/simulation/20260313053648/`

## Re-running Periodically

1. Run `./.agents/skills/running-goji-simulation/scripts/run-e2e.sh` from this repo root.
2. Inspect `summary.md` and `result.env` in the generated run directory.
3. If failures occur, inspect `goji-logs/goji.log` and PR details for the failing run.
