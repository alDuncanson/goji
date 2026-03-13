---
tracker:
  kind: github
  repo: $GOJI_GITHUB_REPO
  active_states:
    - Todo
    - In Progress
    - Rework
    - Human Review
    - Merging
  terminal_states:
    - Done
    - Closed
    - Cancelled
    - Canceled
    - Duplicate
  state_label_prefix: "state:"
  priority_label_prefix: "priority:"
  blocked_by_label_prefix: "blocked-by:"
polling:
  interval_ms: 5000
workspace:
  root: ~/code/goji-workspaces
hooks:
  after_create: |
    git clone --depth 1 "$GOJI_SOURCE_REPO_URL" .
  before_run: |
    git fetch --all --prune
  after_run: |
    git status --short
agent:
  max_concurrent_agents: 6
  max_turns: 12
  max_retry_backoff_ms: 300000
  max_concurrent_agents_by_state:
    in progress: 6
    rework: 3
codex:
  command: amp --execute --stream-json --mode deep
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
runner:
  # Optional extension block for non-Codex CLIs.
  # command overrides codex.command when set.
  command: amp --execute --stream-json --mode deep
  prompt_mode: stdin
  output_format: amp_stream_json
  env:
    AMP_API_KEY: $AMP_API_KEY
---

You are operating as an unattended coding agent in a goji-managed workspace.

Issue context:

- Identifier: {{ issue.identifier }}
- Title: {{ issue.title }}
- State: {{ issue.state }}
- URL: {{ issue.url }}
- Priority: {{ issue.priority }}
- Labels: {{ issue.labels }}

{% if attempt %}
Retry/continuation context:

- This run is attempt {{ attempt }}.
- Resume from the current workspace state.
- Do not repeat completed investigation unless new evidence invalidates it.
{% endif %}

Execution contract:

1. Work only in `{{ issue.identifier }}` workspace.
2. Keep issue and PR artifacts synchronized with actual progress.
3. Run relevant validation before proposing completion.
4. If blocked, provide explicit blocker evidence and required unblock action.
5. Minimize scope creep; create follow-up issues for unrelated improvements.

Harness expectations:

- Prefer deterministic, scriptable commands over interactive flows.
- Keep commit history coherent and review-friendly.
- Treat secrets and privileged operations conservatively.
