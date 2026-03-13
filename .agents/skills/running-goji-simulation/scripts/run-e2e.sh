#!/usr/bin/env bash

set -euo pipefail

timestamp() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

log() {
  printf '[%s] %s\n' "$(timestamp)" "$*"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log "missing required command: $cmd"
    exit 1
  fi
}

wait_until_eval() {
  local description="$1"
  local timeout_seconds="$2"
  local expression="$3"
  local start
  start=$(date +%s)

  while true; do
    if eval "$expression"; then
      return 0
    fi

    if (( $(date +%s) - start >= timeout_seconds )); then
      log "timeout waiting for: $description"
      return 1
    fi

    sleep 5
  done
}

ensure_labels() {
  gh label create "$STATE_ACTIVE_LABEL" --repo "$SIM_REPO" --color "0E8A16" --description "Issue is eligible for goji dispatch" --force >/dev/null
  gh label create "$STATE_DONE_LABEL" --repo "$SIM_REPO" --color "1D76DB" --description "Issue has completed orchestration" --force >/dev/null
  gh label create "$PRIORITY_LABEL" --repo "$SIM_REPO" --color "B60205" --description "Highest priority" --force >/dev/null
}

ensure_repo() {
  if gh repo view "$SIM_REPO" >/dev/null 2>&1; then
    return
  fi

  log "creating simulation repo $SIM_REPO"
  gh repo create "$SIM_REPO" --private --description "Simulation target for goji end-to-end orchestration tests" --add-readme >/dev/null
}

bootstrap_repo() {
  local tmp_dir
  tmp_dir=$(mktemp -d)
  trap 'rm -rf "$tmp_dir"' RETURN

  git clone --depth 1 "$SIM_REPO_URL" "$tmp_dir/repo" >/dev/null 2>&1

  pushd "$tmp_dir/repo" >/dev/null

  cat > go.mod <<'BASE_GO_MOD'
module github.com/alDuncanson/goji-simulation

go 1.22
BASE_GO_MOD

  mkdir -p mathops .github/workflows

  cat > mathops/add.go <<'BASE_ADD_GO'
package mathops

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a + b
}
BASE_ADD_GO

  cat > mathops/add_test.go <<'BASE_ADD_TEST'
package mathops

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}
BASE_ADD_TEST

  cat > .github/workflows/ci.yml <<'BASE_CI'
name: ci

on:
  pull_request:
  push:
    branches:
      - main

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: go test ./...
BASE_CI

  cat > README.md <<'BASE_README'
# goji-simulation

Simulation repository used to validate end-to-end `goji` orchestration flows.

CI runs `go test ./...` for pushes and pull requests.
BASE_README

  go test ./... >/dev/null

  git add go.mod README.md mathops/add.go mathops/add_test.go .github/workflows/ci.yml

  if ! git diff --cached --quiet; then
    if [[ -z "$(git config user.name || true)" ]]; then
      git config user.name "goji-simulation-runner"
    fi
    if [[ -z "$(git config user.email || true)" ]]; then
      git config user.email "goji-simulation-runner@users.noreply.github.com"
    fi

    git commit -m "chore: bootstrap goji simulation baseline" >/dev/null
    git push origin "$DEFAULT_BRANCH" >/dev/null
  fi

  popd >/dev/null
  rm -rf "$tmp_dir"
  trap - RETURN
}

write_issue_body() {
  local issue_number="$1"

  cat > "$ISSUE_BODY_FILE" <<EOF_ISSUE
## Simulation Task

This issue is part of an end-to-end goji orchestration validation run.

### Required implementation

1. Create directory \`simrun\` if it does not exist.
2. Add \`simrun/${TOKEN}_proof.go\` with package \`simrun\` and one exported function \`Token() string\` returning exactly \`${TOKEN}\`.
3. Add \`simrun/${TOKEN}_proof_test.go\` verifying \`Token()\` returns \`${TOKEN}\`.
4. Run \`go test ./...\` and ensure it passes.

### Git + GitHub workflow

1. Create a branch for this issue.
2. Commit the changes and push to origin.
3. Open a pull request with title: \`sim: prove orchestration for ${TOKEN}\`.
4. Include \`Fixes #${issue_number}\` in the PR body.
5. Add an **Isolation Info** section in the PR body containing contents of \`.goji_isolation.txt\` if present.
6. After opening the PR, update this issue labels:
   - remove \`${STATE_ACTIVE_LABEL}\`
   - add \`${STATE_DONE_LABEL}\`

Keep scope tightly limited to this issue.
EOF_ISSUE
}

write_workflow() {
  cat > "$WORKFLOW_FILE" <<EOF_WORKFLOW
---
tracker:
  kind: github
  repo: ${SIM_REPO}
  active_states:
    - In Progress
  terminal_states:
    - Done
    - Closed
    - Cancelled
    - Canceled
    - Duplicate
  state_label_prefix: "state:"
  priority_label_prefix: "priority:"
  blocked_by_label_prefix: "blocked-by:"
  candidate_limit: 50
polling:
  interval_ms: 3000
workspace:
  root: ${WORKSPACE_ROOT}
hooks:
  after_create: |
    git clone --depth 1 "\$GOJI_SOURCE_REPO_URL" .
  before_run: |
    {
      echo "timestamp=\$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      echo "workspace=\$(pwd)"
      echo "hostname=\$(hostname)"
      echo "whoami=\$(whoami)"
      echo "uname=\$(uname -a)"
      echo "repo=\$(git remote get-url origin)"
      echo "branch=\$(git branch --show-current || true)"
    } > .goji_isolation.txt
  after_run: |
    git status --short
agent:
  max_concurrent_agents: 1
  max_turns: 6
  max_retry_backoff_ms: 60000
  max_concurrent_agents_by_state:
    in progress: 1
runner:
  command: amp --execute --stream-json --mode deep
  prompt_mode: stdin
  output_format: amp_stream_json
  turn_timeout_ms: 1800000
  read_timeout_ms: 5000
  stall_timeout_ms: 600000
---

You are the coding agent for issue {{ issue.identifier }} in repo \`${SIM_REPO}\`.

Hard requirements:

1. Complete exactly the requested issue scope and nothing else.
2. Run \`go test ./...\` before opening a PR.
3. Use \`gh\` CLI non-interactively to create the PR.
4. Ensure PR body includes \`Fixes {{ issue.identifier }}\`.
5. Include \`.goji_isolation.txt\` contents in an \`Isolation Info\` section in the PR body when present.
6. After opening the PR, remove label \`${STATE_ACTIVE_LABEL}\` and add label \`${STATE_DONE_LABEL}\` on the issue.
7. Keep changes small and deterministic.

Git workflow:

1. Create a branch named \`goji-sim/{{ issue.number }}-{{ issue.title | downcase | replace: ' ', '-' | replace: ':', '' }}\`.
2. Commit with a concise message.
3. Push branch to origin.
4. Open PR against \`${DEFAULT_BRANCH}\`.

Stop when PR is opened and issue label state transition is complete.
EOF_WORKFLOW
}

cleanup_goji() {
  if [[ -n "${GOJI_PID:-}" ]] && kill -0 "$GOJI_PID" >/dev/null 2>&1; then
    kill "$GOJI_PID" >/dev/null 2>&1 || true
    wait "$GOJI_PID" >/dev/null 2>&1 || true
  fi
}

require_cmd gh
require_cmd git
require_cmd go
require_cmd amp
require_cmd rg

PROJECT_ROOT="${GOJI_PROJECT_ROOT:-$(pwd)}"
SIM_REPO="${GOJI_SIM_REPO:-}"
if [[ -z "$SIM_REPO" ]]; then
  SIM_REPO="$(gh api user --jq .login)/goji-simulation"
fi

SIM_REPO_URL="https://github.com/${SIM_REPO}.git"
DEFAULT_BRANCH="$(gh repo view "$SIM_REPO" --json defaultBranchRef --jq '.defaultBranchRef.name' 2>/dev/null || echo "main")"
STATE_ACTIVE_LABEL="${GOJI_ACTIVE_LABEL:-state:in-progress}"
STATE_DONE_LABEL="${GOJI_DONE_LABEL:-state:done}"
PRIORITY_LABEL="${GOJI_PRIORITY_LABEL:-priority:1}"
RUN_TIMEOUT_SECONDS="${GOJI_SIM_RUN_SECONDS:-900}"
TOKEN_PREFIX="${GOJI_SIM_TOKEN_PREFIX:-simtoken}"
GOJI_CMD="${GOJI_CMD:-go run ./cmd/goji run}"
WORKSPACE_ROOT="${GOJI_SIM_WORKSPACE_ROOT:-/tmp/goji-simulation-workspaces}"
ARTIFACT_ROOT="${GOJI_SIM_ARTIFACT_ROOT:-${PROJECT_ROOT}/log/simulation}"

RUN_ID="$(date -u +%Y%m%d%H%M%S)"
TOKEN="${TOKEN_PREFIX}-${RUN_ID}"
RUN_DIR="${ARTIFACT_ROOT}/${RUN_ID}"
WORKFLOW_FILE="${RUN_DIR}/WORKFLOW.md"
ISSUE_BODY_FILE="${RUN_DIR}/issue.md"
PR_BODY_FILE="${RUN_DIR}/pr-body.md"
ISOLATION_INFO_FILE="${RUN_DIR}/isolation-info.txt"
SUMMARY_FILE="${RUN_DIR}/summary.md"
RESULT_ENV_FILE="${RUN_DIR}/result.env"
GOJI_LOG_ROOT="${RUN_DIR}/goji-logs"
GOJI_LOG_FILE="${GOJI_LOG_ROOT}/goji.log"
GOJI_PROCESS_LOG="${RUN_DIR}/goji-process.log"

GOJI_PID=""
trap cleanup_goji EXIT INT TERM

mkdir -p "$RUN_DIR" "$GOJI_LOG_ROOT"

log "simulation run_id=${RUN_ID} repo=${SIM_REPO}"

ensure_repo
DEFAULT_BRANCH="$(gh repo view "$SIM_REPO" --json defaultBranchRef --jq '.defaultBranchRef.name')"
ensure_labels
bootstrap_repo

write_issue_body "__pending__"
ISSUE_URL="$(gh issue create --repo "$SIM_REPO" --title "Simulation: ${TOKEN}" --body-file "$ISSUE_BODY_FILE" --label "$PRIORITY_LABEL")"
ISSUE_NUMBER="$(basename "$ISSUE_URL")"
ISSUE_IDENTIFIER="#${ISSUE_NUMBER}"

write_issue_body "$ISSUE_NUMBER"
gh issue edit "$ISSUE_NUMBER" --repo "$SIM_REPO" --body-file "$ISSUE_BODY_FILE" >/dev/null

write_workflow

log "starting goji headless process"
pushd "$PROJECT_ROOT" >/dev/null
GOJI_SOURCE_REPO_URL="$SIM_REPO_URL" nohup bash -lc "${GOJI_CMD} --no-tui --logs-root '${GOJI_LOG_ROOT}' '${WORKFLOW_FILE}'" >"$GOJI_PROCESS_LOG" 2>&1 &
GOJI_PID=$!
popd >/dev/null

wait_until_eval "goji log initialization" 90 "[[ -f '$GOJI_LOG_FILE' ]]"

sleep 10
if rg -q 'issue_dispatched' "$GOJI_LOG_FILE"; then
  log "issue dispatched before active label was applied"
  exit 1
fi

log "applying active-state label ${STATE_ACTIVE_LABEL} to issue ${ISSUE_IDENTIFIER}"
gh issue edit "$ISSUE_NUMBER" --repo "$SIM_REPO" --add-label "$STATE_ACTIVE_LABEL" >/dev/null

wait_until_eval "issue dispatch" "$RUN_TIMEOUT_SECONDS" "rg -q 'issue_dispatched' '$GOJI_LOG_FILE'"

PR_NUMBER=""
PR_URL=""
start_time=$(date +%s)
while true; do
  PR_NUMBER=$(gh pr list --repo "$SIM_REPO" --state all --search "$TOKEN in:title" --json number --jq '.[0].number // empty' 2>/dev/null || true)
  if [[ -n "$PR_NUMBER" ]]; then
    PR_URL="https://github.com/${SIM_REPO}/pull/${PR_NUMBER}"
    break
  fi

  if (( $(date +%s) - start_time >= RUN_TIMEOUT_SECONDS )); then
    log "timeout waiting for pull request creation"
    exit 1
  fi

  sleep 8
done

log "pull request created: ${PR_URL}"

CHECK_STATUS=""
CHECK_CONCLUSION=""
start_time=$(date +%s)
while true; do
  CHECK_STATUS=$(gh pr view "$PR_NUMBER" --repo "$SIM_REPO" --json statusCheckRollup --jq '.statusCheckRollup[0].status // ""' 2>/dev/null || true)
  CHECK_CONCLUSION=$(gh pr view "$PR_NUMBER" --repo "$SIM_REPO" --json statusCheckRollup --jq '.statusCheckRollup[0].conclusion // ""' 2>/dev/null || true)

  if [[ "$CHECK_STATUS" == "COMPLETED" && "$CHECK_CONCLUSION" == "SUCCESS" ]]; then
    break
  fi

  if [[ "$CHECK_STATUS" == "COMPLETED" && "$CHECK_CONCLUSION" != "SUCCESS" ]]; then
    log "CI check failed for PR #${PR_NUMBER} (conclusion=${CHECK_CONCLUSION})"
    exit 1
  fi

  if (( $(date +%s) - start_time >= RUN_TIMEOUT_SECONDS )); then
    log "timeout waiting for CI checks on PR #${PR_NUMBER}"
    exit 1
  fi

  sleep 8
done

log "CI checks passed for PR #${PR_NUMBER}"

PR_STATE=$(gh pr view "$PR_NUMBER" --repo "$SIM_REPO" --json state --jq '.state')
if [[ "$PR_STATE" != "MERGED" ]]; then
  gh pr merge "$PR_NUMBER" --repo "$SIM_REPO" --squash --delete-branch --subject "sim: prove orchestration for ${TOKEN}" --body "Merging successful goji end-to-end simulation run." >/dev/null
fi

start_time=$(date +%s)
ISSUE_STATE=""
while true; do
  ISSUE_STATE=$(gh issue view "$ISSUE_NUMBER" --repo "$SIM_REPO" --json state --jq '.state' 2>/dev/null || true)
  if [[ "$ISSUE_STATE" == "CLOSED" ]]; then
    break
  fi

  if (( $(date +%s) - start_time >= RUN_TIMEOUT_SECONDS )); then
    log "timeout waiting for issue ${ISSUE_IDENTIFIER} to close"
    exit 1
  fi

  sleep 6
done

gh pr view "$PR_NUMBER" --repo "$SIM_REPO" --json body --jq '.body' > "$PR_BODY_FILE"
if rg -q '^## Isolation Info' "$PR_BODY_FILE"; then
  awk '
    /^## Isolation Info/ { section = 1; next }
    section {
      if (!started && $0 ~ /^[[:space:]]*$/) {
        next
      }
      started = 1
      print
    }
  ' "$PR_BODY_FILE" > "$ISOLATION_INFO_FILE"
fi
if [[ ! -s "$ISOLATION_INFO_FILE" ]]; then
  rm -f "$ISOLATION_INFO_FILE"
fi

WORKSPACE_PATH=$(rg '"event":"workspace_ready"' "$GOJI_LOG_FILE" | tail -n 1 | sed -E 's/.*"message":"([^"]+)".*/\1/' || true)
ISOLATION_FILE=""
if [[ -n "$WORKSPACE_PATH" && -f "$WORKSPACE_PATH/.goji_isolation.txt" ]]; then
  ISOLATION_FILE="$WORKSPACE_PATH/.goji_isolation.txt"
fi
if [[ -z "$ISOLATION_FILE" && -s "$ISOLATION_INFO_FILE" ]]; then
  ISOLATION_FILE="$ISOLATION_INFO_FILE"
fi

cat > "$RESULT_ENV_FILE" <<EOF_RESULT
RUN_ID=${RUN_ID}
TOKEN=${TOKEN}
REPO=${SIM_REPO}
ISSUE_NUMBER=${ISSUE_NUMBER}
ISSUE_URL=${ISSUE_URL}
PR_NUMBER=${PR_NUMBER}
PR_URL=${PR_URL}
PR_BODY_FILE=${PR_BODY_FILE}
WORKSPACE_PATH=${WORKSPACE_PATH}
ISOLATION_FILE=${ISOLATION_FILE}
GOJI_LOG_FILE=${GOJI_LOG_FILE}
GOJI_PROCESS_LOG=${GOJI_PROCESS_LOG}
EOF_RESULT

cat > "$SUMMARY_FILE" <<EOF_SUMMARY
# goji Simulation Run ${RUN_ID}

1. Repository: https://github.com/${SIM_REPO}
2. Token: ${TOKEN}
3. Issue: ${ISSUE_URL}
4. Pull Request: ${PR_URL}
5. goji Log: ${GOJI_LOG_FILE}
6. goji Process Log: ${GOJI_PROCESS_LOG}
7. PR Body: ${PR_BODY_FILE}
8. Workspace Path: ${WORKSPACE_PATH}
9. Isolation Evidence: ${ISOLATION_FILE}

## Assertions

1. Issue was not dispatched before \`${STATE_ACTIVE_LABEL}\` label was applied.
2. Issue was dispatched after label application.
3. Agent created and pushed a branch, opened a PR, and included issue linkage.
4. PR CI checks reached success.
5. PR was merged.
6. Issue reached closed state.
EOF_SUMMARY

cleanup_goji

log "simulation complete"
log "summary=${SUMMARY_FILE}"
log "result_env=${RESULT_ENV_FILE}"
