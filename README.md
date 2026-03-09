# AIops For AI Coding Configuration

This repository is now oriented around an `AI coding configuration operations platform`.

The product shape is:

- `Local CLI Agent`
  - registers a device
  - uploads config snapshots and session summaries
  - pulls only approved change plans
  - applies local changes safely and can roll them back
- `Cloud Research Agent`
  - analyzes token usage, raw query history, and config snapshots
  - generates ranked instruction recommendations and structured change plans
  - stays local and uses only uploaded personal usage data in this MVP
- `AIops Server`
  - stores metrics, recommendations, change plans, execution history, impact, and audit logs
- `Web Dashboard`
  - shows a user-facing summary of recommendations and workspace health
  - approves or rejects change plans
  - inspects rollout queue and token-based before/after impact without exposing raw low-level internals

Detailed codebase documentation:

- [`docs/CODEBASE.md`](/Users/doyechan/Desktop/codes/aiops/docs/CODEBASE.md)

## What changed

- Recommendation requests now create `change plans` in `awaiting_review`
- Low-risk single-file config merges can be `auto-approved` by the policy engine
- Only approved plans appear in the local execution queue
- The dashboard now favors a user-facing approval surface instead of a developer-style operations console
- Session summaries now focus on token usage and raw query history for MVP research analysis
- `agentopt session` auto-collects the latest local Codex session from `~/.codex/sessions` when `--file` is omitted
- `agentopt session --recent N` uploads the most recent `N` local Codex sessions in chronological order
- Local apply supports both `JSON merge patches` and safe `text append` patches such as `AGENTS.md`

## Quickstart

```bash
make generate
make run
```

In another shell:

```bash
go run ./cmd/agentopt login --server http://127.0.0.1:8082 --token agentopt-dev-token --org demo-org --user demo-user
go run ./cmd/agentopt connect --project demo-repo --repo-path .
go run ./cmd/agentopt snapshot --file examples/config-snapshot.json
go run ./cmd/agentopt session
go run ./cmd/agentopt session --recent 5
go run ./cmd/agentopt recommendations
go run ./cmd/agentopt apply --recommendation-id <RECOMMENDATION_ID>
go run ./cmd/agentopt preflight --apply-id <CHANGE_PLAN_ID>
go run ./cmd/agentopt review --apply-id <CHANGE_PLAN_ID> --decision approve
go run ./cmd/agentopt sync
go run ./cmd/agentopt rollback --apply-id <CHANGE_PLAN_ID>
go run ./cmd/agentopt history
go run ./cmd/agentopt impact
go run ./cmd/agentopt audit
```

You can still use:

```bash
go run ./cmd/agentopt apply --recommendation-id <RECOMMENDATION_ID> --yes
```

That path is useful for development because it creates, approves, and applies the plan locally in one step.

Then open `http://127.0.0.1:8082/dashboard`, enter `agentopt-dev-token`, `demo-org`, and the target `project_id`.

## Research Agent MVP

The cloud research agent is intentionally narrow in this MVP:

- provider metadata is surfaced as `local`
- no live OpenAI API call or web search is made yet
- recommendation generation only looks at uploaded token usage and raw query history
- recommendation output is limited to instruction/custom-rule suggestions for now
- the local executor now enforces a strict file allowlist before any approved plan is applied
- the local executor can now apply and roll back multi-step plans across allowlisted files
