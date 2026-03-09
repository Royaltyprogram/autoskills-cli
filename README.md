# AI Coding Setup Analytics SaaS

Thin CLI + server prototype for measuring AI coding usage, generating configuration recommendations, and applying local changes with auditability.

## What is included

- Server API for agent registration, project connection, config snapshots, session summaries, recommendations, apply plans, and dashboard metrics
- In-memory analytics engine with simple recommendation rules
- `agentopt` CLI for login, connect, ingest, inspect recommendations, and apply JSON config updates locally
- Minimal web dashboard at `/dashboard` for quick inspection of org metrics and ranked recommendations
- Sample payloads in [`examples/config-snapshot.json`](/Users/doyechan/Desktop/codes/aiops/examples/config-snapshot.json) and [`examples/session-summary.json`](/Users/doyechan/Desktop/codes/aiops/examples/session-summary.json)

## Quickstart

```bash
make generate
make run
```

In another shell:

```bash
go run ./cmd/agentopt login --server http://127.0.0.1:8082 --org demo-org --user demo-user
go run ./cmd/agentopt connect --project demo-repo --repo-path .
go run ./cmd/agentopt snapshot --file examples/config-snapshot.json
go run ./cmd/agentopt session --file examples/session-summary.json
go run ./cmd/agentopt status
go run ./cmd/agentopt projects
go run ./cmd/agentopt recommendations
go run ./cmd/agentopt apply --recommendation-id <ID_FROM_RECOMMENDATIONS> --yes
go run ./cmd/agentopt rollback --apply-id <APPLY_ID>
go run ./cmd/agentopt history
go run ./cmd/agentopt impact
go run ./cmd/agentopt audit
```

Then open `http://127.0.0.1:8082/dashboard`, enter `demo-org` and the returned `project_id`, and inspect the temporary dashboard.

The server currently keeps state in memory so it is suitable for prototyping the server/CLI contract before moving to persistent storage and a real dashboard.
