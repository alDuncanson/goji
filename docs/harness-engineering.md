# Harness Engineering For goji

`goji` is most effective when your repo is engineered for unattended execution. This guide focuses on practical harness controls.

## 1) Deterministic Workspace Bootstrap

1. Put all workspace initialization into `hooks.after_create`.
2. Keep bootstrap idempotent: reruns should not corrupt the workspace.
3. Fail fast on missing credentials, toolchains, or package registries.
4. Keep bootstrap output concise; long logs should be redirected to files under workspace.

## 2) Deterministic Validation Contract

1. Define one canonical CI-equivalent validation command.
2. Ensure it can run non-interactively.
3. Prefer strict checks over ambiguous “best effort” scripts.
4. Make failures machine-readable where possible.

## 3) Prompt Policy Is Runtime Policy

Your prompt body is your policy layer. Require the agent to:

1. Update ticket/PR artifacts consistently.
2. Capture validation evidence explicitly.
3. Use retry-safe behavior and avoid destructive resets.
4. Escalate only when truly blocked on missing auth or missing tools.

## 4) Principle of Least Privilege

1. Give the runtime only the tokens and scopes it needs.
2. Prefer repo-scoped credentials over org-global credentials.
3. Keep agent command permissions constrained by sandbox and host controls.
4. Treat hooks as privileged code; review them like production scripts.

## 5) Containment And Safety

1. Keep `workspace.root` on isolated storage where possible.
2. Run `goji` under a dedicated OS user for production daemons.
3. Avoid sharing mutable global caches unless required.
4. Keep system-level destructive binaries out of `PATH` for the runtime user where practical.

## 6) Observability And Operations

1. Keep TUI open during initial rollout to watch retry/reconciliation behavior.
2. Stream JSON logs into your logging stack for alerting on validation failures and retry storms.
3. Watch for prolonged `RetryQueued` entries and stalled-turn signals.
4. Use `r` in TUI for immediate poll/reconcile trigger during incident response.
