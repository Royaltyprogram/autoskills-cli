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
- Local apply is executed through a `Codex SDK` runner while preflight, allowlist checks, backup, and rollback stay in the Go CLI

## Quickstart

```bash
make generate
make install-codex-runner
make run
```

`make run` now uses `configs/local.yaml`, which keeps the local demo account enabled for development. Closed beta or production deployments should run with `APP_MODE=prod` plus seeded beta users and secrets supplied through env vars.

In another shell:

```bash
go run ./cmd/agentopt login --server http://127.0.0.1:8082 --token <CLI_TOKEN_FROM_DASHBOARD>
go run ./cmd/agentopt connect --project demo-repo --repo-path .
go run ./cmd/agentopt projects
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

For local development, open `http://127.0.0.1:8082/`, sign in with `demo@example.com / demo1234`, issue a CLI token from the dashboard, and run `agentopt login --server http://127.0.0.1:8082` on the machine you want to connect. The CLI prompts for the issued token if `--token` is omitted.

For closed beta or production, disable the demo path and seed named beta accounts through env:

```bash
APP_MODE=prod \
JWT_SECRET=replace-me \
AUTH_BOOTSTRAP_USERS_JSON='[{"id":"beta-user-1","org_id":"beta-org","org_name":"Beta Org","email":"beta1@example.com","name":"Beta Operator","password":"replace-me"}]' \
go run .
```

In this MVP every connected repository shares one workspace per organization. `agentopt connect` keeps that shared workspace current, and `pending`, `sync`, `history`, and `impact` always read from the same rollout stream.

If `sync` or `apply --yes` fails before the plan starts, check the local runner first:

```bash
make check-codex-runner
```

If you want to force a lower Codex reasoning effort for local apply, pass it through the CLI or env:

```bash
go run ./cmd/agentopt sync --codex-reasoning-effort low
AGENTOPT_CODEX_REASONING_EFFORT=low go run ./cmd/agentopt apply --recommendation-id <RECOMMENDATION_ID> --yes
```

To rerun the mock dashboard approve -> local agent sync -> rollback flow without touching your real workspace:

```bash
make mock-e2e
```

That test starts the real analytics routes in-process, issues a dashboard CLI token, approves a change plan through the web auth flow, runs `agentopt sync` with a stub Codex runner against a temp workspace, verifies the file change, and rolls it back.

For closed beta deployment checks, the server also exposes:

- `GET /healthz` for liveness
- `GET /readyz` for readiness including analytics-store database access

And you can run the end-to-end smoke from this repo root after exporting a seeded beta account:

```bash
export BETA_SMOKE_EMAIL=beta1@example.com
export BETA_SMOKE_PASSWORD=replace-me
make closed-beta-smoke
```

For repeatable closed beta verification in CI:

```bash
make ci-beta
```

To build the CLI artifact you hand to beta users:

```bash
make beta-cli-bundle
VERSION_LABEL=0.1.0-beta.1 make beta-cli-bundle
```

That command produces `output/release/agentopt-<version>-<os>-<arch>.tar.gz` with:

- `agentopt`
- `tools/codex-runner/run.mjs`
- the pinned Node dependencies required for local apply

The bundled CLI also answers `./agentopt version`, and `make build` now embeds git version metadata into `output/agentopt`.

## Container Deploy

The container now defaults to:

- `APP_MODE=prod`
- SQLite state at `/app/data/agentopt.db`
- stdout request/application logs

Build and run it with a seeded beta account:

```bash
docker build -t agentopt-beta .
docker run --rm -p 8082:8082 \
  -v "$PWD/.runtime-data:/app/data" \
  -e JWT_SECRET=replace-me \
  -e AUTH_BOOTSTRAP_USERS_JSON='[{"id":"beta-user-1","org_id":"beta-org","org_name":"Beta Org","email":"beta1@example.com","name":"Beta Operator","password":"replace-me"}]' \
  agentopt-beta
```

For MySQL-backed deployment, override the DB env at runtime:

```bash
docker run --rm -p 8082:8082 \
  -e DB_DIALECT=mysql \
  -e DB_DSN='user:pass@tcp(mysql:3306)/agentopt?charset=utf8mb4&parseTime=True&loc=UTC' \
  -e JWT_SECRET=replace-me \
  -e AUTH_BOOTSTRAP_USERS_JSON='[...]' \
  agentopt-beta
```

The repository also includes `.github/workflows/beta-ci.yml`, which runs `make ci-beta` and then boots the local server to execute `scripts/closed_beta_smoke.sh` with the local demo account.

For a stricter closed beta rollout, you can also restrict network access in-app:

```bash
HTTP_ALLOWED_CIDRS='203.0.113.10/32,198.51.100.0/24' \
HTTP_TRUSTED_PROXY_CIDRS='10.0.0.0/8' \
go run .
```

If `HTTP_TRUSTED_PROXY_CIDRS` is empty, AgentOpt only trusts the direct socket remote address and ignores forwarded IP headers.

`APP_MODE=prod` now fails fast during startup if critical closed beta settings are unsafe or incomplete, including a missing `JWT_SECRET`, invalid CIDR values, demo-user enablement, static token bypass enablement, or malformed bootstrap users.

## Research Agent MVP

The cloud research agent is intentionally narrow in this MVP:

- provider metadata is surfaced as `local`
- no live OpenAI API call or web search is made yet
- recommendation generation only looks at uploaded token usage and raw query history
- recommendation output is limited to instruction/custom-rule suggestions for now
- the local executor now enforces a strict file allowlist before any approved plan is applied
- the local executor can now apply and roll back multi-step plans across allowlisted files
- the actual local file edit step is delegated to `Codex SDK`, but the CLI still owns preflight, backup, result reporting, and rollback
