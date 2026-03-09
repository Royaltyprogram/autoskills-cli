# AIops For AI Coding Configuration

This repository is now oriented around an `AI coding configuration operations platform`.

The product shape is:

- `Local CLI Agent`
  - registers a device
  - uploads config snapshots and session summaries
  - pulls only approved change plans
  - applies local changes safely and can roll them back
- `Cloud Research Agent`
  - analyzes structured metrics and config snapshots
  - generates ranked recommendations and structured change plans
  - is wired as an `OpenAI placeholder` in this development build
- `AIops Server`
  - stores metrics, recommendations, change plans, execution history, impact, and audit logs
- `Web Dashboard`
  - reviews recommendations
  - approves or rejects change plans
  - inspects rollout queue, impact, and audit history

Detailed codebase documentation:

- [`docs/CODEBASE.md`](/Users/doyechan/Desktop/codes/aiops/docs/CODEBASE.md)

## What changed

- Recommendation requests now create `change plans` in `awaiting_review`
- Only approved plans appear in the local execution queue
- The dashboard exposes a `review queue` and `approved execution queue`
- Config snapshots and session summaries now carry richer config metrics and derived features
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
go run ./cmd/agentopt session --file examples/session-summary.json
go run ./cmd/agentopt recommendations
go run ./cmd/agentopt apply --recommendation-id <RECOMMENDATION_ID>
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

## OpenAI placeholder

The cloud research agent is intentionally implemented as a placeholder:

- provider metadata is surfaced as `openai`
- no live OpenAI API call is made yet
- recommendation generation remains deterministic and metrics-driven until the API integration is enabled
