# AIops For AI Coding Configuration

This repository is now oriented around an `AI coding configuration operations platform`.

The product shape is:

- `Local CLI Agent`
  - registers a device
  - uploads config snapshots and session summaries
- `Cloud Research Agent`
  - analyzes token usage, raw query history, and config snapshots
  - generates ranked workflow feedback reports for the user
  - stays local and uses only uploaded personal usage data in this MVP
- `AIops Server`
  - stores metrics, feedback reports, research status, and audit logs
- `Web Dashboard`
  - shows a user-facing summary of feedback reports and workspace health
  - helps the user inspect prompt quality, workflow friction, and usage trends without auto-applying changes

Detailed codebase documentation:

- [`docs/CODEBASE.md`](/Users/doyechan/Desktop/codes/aiops/docs/CODEBASE.md)
- [`docs/CLOSED_BETA_RUNBOOK.md`](/Users/doyechan/Desktop/codes/aiops/docs/CLOSED_BETA_RUNBOOK.md)
- [`docs/LOCAL_MANUAL_E2E.md`](/Users/doyechan/Desktop/codes/aiops/docs/LOCAL_MANUAL_E2E.md)

## What changed

- Reports are now read-only feedback reports instead of local patch plans
- The dashboard now favors a user-facing report surface instead of an approval console
- Session summaries now focus on token usage and raw query history for MVP research analysis
- `crux setup` now compresses onboarding into one command: login, workspace connect, initial local upload, and background collection enrollment when supported
- `crux session` auto-collects the latest local Codex session from `~/.codex/sessions` when `--file` is omitted
- `crux session --recent N` uploads the most recent `N` local Codex sessions in chronological order
- `crux collect` uploads session data now and can skip unchanged snapshots by default
- `crux collect --watch` watches local Codex session files, uploads each new logical session after the saved cursor, and keeps the interval as a fallback scan
- Feedback reports now wait until at least `10` uploaded sessions exist before the server publishes the first report

## Quickstart

```bash
make generate
make run
```

`make run` now uses `configs/local.yaml`, which keeps local development defaults such as the static API token enabled. The dashboard itself still uses Google sign-in, so local web login requires Google OAuth credentials. Closed beta or production deployments should run with `APP_MODE=prod` plus seeded beta users and secrets supplied through env vars.

If you want a no-GCP local web login path for development, use:

```bash
make run-local-google-stub
```

That target starts a local OAuth stub and the server together. Open `http://127.0.0.1:8082/`, click `Continue with Google`, and you will sign in as the stub account. Override `GOOGLE_STUB_EMAIL` or `GOOGLE_STUB_NAME` if you want a different local identity.

`App.StorePath` remains only as the legacy JSON import location and SQLite fallback path seed. The live runtime store is `DB.DSN`.

In another shell:

For source development:

```bash
go run ./cmd/crux setup --server http://127.0.0.1:8082 --token <CLI_TOKEN_FROM_DASHBOARD>
go run ./cmd/crux workspace
go run ./cmd/crux snapshot --file examples/config-snapshot.json
go run ./cmd/crux session
go run ./cmd/crux session --recent 5
go run ./cmd/crux collect --watch --recent 1 --interval 30m
go run ./cmd/crux reports
go run ./cmd/crux audit
```

If you want the plain `crux` command to point at the current repository build instead of the latest published release, install the local dev build explicitly:

```bash
./scripts/install_local_dev.sh
crux reset
crux version
```

This path updates `~/.local/bin/crux` to the current repo build under `~/.local/share/crux/current`. Use it when validating unreleased CLI changes locally.

For beta or production user machines, install the released CLI and run `crux` directly instead of `go run`:

```bash
curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/crux-cli/main/scripts/install.sh | sh
crux setup
```

Release installs use a prebuilt binary, so Go is not required. If your shell cannot find `crux`, add `~/.local/bin` to `PATH`.
On supported installed macOS environments, `crux setup` also enrolls background collection automatically. On other environments it prints the manual fallback command, typically `crux collect --watch --recent 1 --interval 30m`.
After setup, plain `crux` now works as the default entrypoint: it shows the setup hint when the CLI is not configured yet, and otherwise prints the current shared-workspace status.

For local development, set `AUTH_GOOGLE_CLIENT_ID` and `AUTH_GOOGLE_CLIENT_SECRET`, open `http://127.0.0.1:8082/`, click `Continue with Google`, issue a CLI token from the dashboard, and run `crux setup --server http://127.0.0.1:8082` on the machine you want to connect. The CLI prompts for the issued token if `--token` is omitted and automatically registers the device, connects the current repo, and uploads an initial snapshot plus local Codex session history on first setup.

For closed beta or production, disable the demo path and seed named beta accounts through env:

```bash
APP_MODE=prod \
JWT_SECRET=replace-me \
AUTH_GOOGLE_CLIENT_ID=replace-me \
AUTH_GOOGLE_CLIENT_SECRET=replace-me \
AUTH_BOOTSTRAP_USERS_JSON='[{"id":"beta-user-1","org_id":"beta-org","org_name":"Beta Org","email":"beta1@example.com","name":"Beta Operator","role":"admin"}]' \
go run .
```

`AUTH_BOOTSTRAP_USERS_JSON` is optional. Use it when you want a Google account with a matching email to join a pre-seeded org or take a preassigned role. Without it, the first Google sign-in creates a new admin workspace automatically.

If you would rather mount a secret file than inline JSON in env, you can use:

```bash
APP_MODE=prod \
JWT_SECRET_FILE=/run/secrets/crux-jwt-secret \
AUTH_GOOGLE_CLIENT_ID_FILE=/run/secrets/crux-google-client-id \
AUTH_GOOGLE_CLIENT_SECRET_FILE=/run/secrets/crux-google-client-secret \
AUTH_BOOTSTRAP_USERS_FILE=/run/secrets/crux-beta-users.json \
OPENAI_API_KEY_FILE=/run/secrets/crux-openai-api-key \
go run .
```

Supported secret file envs now include `JWT_SECRET_FILE`, `DB_DSN_FILE`, `APP_API_TOKEN_FILE`, `AUTH_BOOTSTRAP_USERS_FILE`, `AUTH_GOOGLE_CLIENT_ID_FILE`, and `AUTH_GOOGLE_CLIENT_SECRET_FILE`. File-based values override the plain env form when both are set.
`OPENAI_API_KEY_FILE` is also supported for the cloud research agent.

Bootstrap users are now treated as pre-seeded Google-linked identities: removing a user from the bootstrap file revokes existing tokens, and Google sign-in links to the seeded record when the email matches. Seed bootstrap users when you want multiple people to land in the same org or when you need fixed `admin` or `member` roles before first sign-in.

In this MVP every connected repository shares one workspace per organization. `crux setup` handles the first-time device registration, shared-workspace connection, and initial local Codex session upload, while `crux connect` remains available when you need to reconnect a different repo manually. The generated report records are now user-facing feedback reports rather than executable patch queues.

If you want to keep uploads flowing in the background, keep the collector running:

```bash
crux collect --watch --recent 1 --interval 30m
```

Installed macOS release builds usually do this automatically during `crux setup`. The watcher reacts to session file changes and uses `--interval` only as a fallback scan, while the saved session cursor makes sure every new logical session after the cursor is uploaded. Keep the manual command for source development, Linux hosts, or any environment where setup reports `background.status` as `manual_only` or `failed`.

If you want a one-off manual upload instead, keep using `crux collect --codex-home ~/.codex`.

For closed beta deployment checks, the server also exposes:

- `GET /healthz` for liveness
- `GET /readyz` for readiness including analytics-store database access

`make closed-beta-smoke` expects `BETA_SMOKE_CLI_TOKEN`, which you issue from the dashboard after signing in with Google.

For repeatable closed beta verification in CI:

```bash
make ci-beta
```

To exercise the real `APP_MODE=prod` path locally with file-based secrets, a local OAuth stub, and an ephemeral MySQL container:

```bash
make closed-beta-prod-smoke
```

`make closed-beta-prod-smoke` now bootstraps a local OAuth stub and an ephemeral MySQL container automatically, so it does not require real Google credentials, a browser login, or a pre-provisioned database.

To force that smoke test to use the ignored local secret files in `secrets/` and verify live OpenAI-backed feedback report generation:

```bash
JWT_SECRET_FILE_OVERRIDE=secrets/crux-jwt-secret \
AUTH_BOOTSTRAP_USERS_FILE_OVERRIDE=secrets/crux-beta-users.json \
OPENAI_API_KEY_FILE_OVERRIDE=secrets/crux-openai-api-key \
EXPECT_RESEARCH_MODE=openai_responses_api \
make closed-beta-prod-smoke
```

To build the CLI artifact you hand to beta users:

```bash
make beta-cli-bundle
VERSION_LABEL=0.1.0-beta.1 make beta-cli-bundle
```

That command produces:

- `output/release/crux-<version>-<os>-<arch>.tar.gz`
- `output/release/crux-<version>-<os>-<arch>.tar.gz.sha256`
- `output/release/crux-<version>-<os>-<arch>.json`

And you can validate the latest bundle locally with:

```bash
make verify-beta-bundle
make verify-install-script
```

To build a consolidated release index for the latest version across the manifests currently in `output/release`:

```bash
make build-release-index
```

To publish the built assets to GitHub Releases with the `gh` CLI:

```bash
VERSION_LABEL=0.1.0-beta.1 make publish-github-release
```

`publish-github-release` uploads the versioned bundle archives, checksums, per-platform manifests, and the consolidated release-index file.

To build and publish backend server bundles for GitHub Releases:

```bash
cp .env.server-release.example .env
# edit .env
./scripts/build_and_publish_server_release.sh
```

That script loads values from `.env` by default, builds `crux-server-<version>-<os>-<arch>.tar.gz`, uploads it to the matching GitHub Release, and if the release already exists it only uploads server assets without rewriting the existing release notes. Use `ENV_FILE=/path/to/file.env` if you want a different env file.

The GitHub Actions workflow now updates a rolling `beta` prerelease on every `main` push and still publishes a versioned release automatically when you push a tag such as `0.1.0-beta.1`.

If you use `workflow_dispatch` in GitHub Actions, you can now pass `version`, `draft`, `prerelease`, and `latest` inputs and reuse the same publish path without creating the tag first.

To export or restore the live runtime store against the same `APP_MODE` / `DB_*` / secret-file env that the server uses:

```bash
make store-export OUTPUT=output/runtime-store-backup.json
make store-import INPUT=output/runtime-store-backup.json
```

`store-import` is intentionally gated behind `--yes` because it overwrites the configured runtime store.

The bundle itself contains:

- `crux`

The installed CLI answers `crux version`, and `make build` now embeds git version metadata into `output/crux`.

For a one-command install from GitHub Releases:

```bash
curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/crux-cli/main/scripts/install.sh | sh
CRUX_VERSION=0.1.0-beta.1 curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/crux-cli/main/scripts/install.sh | sh
```

The installer downloads the matching release bundle for the current platform, installs it under `~/.local/share/crux/<version>`, writes `~/.local/bin/crux`, does not require Go on the target machine, and installs a local Node.js runtime automatically when the machine does not already have a compatible one.
After install, the shortest onboarding path is:

```bash
crux setup
```

## Container Deploy

The container now defaults to:

- `APP_MODE=prod`
- legacy JSON import path at `/app/data/crux-store.json`
- no runtime DB DSN; provide `DB_DSN` or `DB_DSN_FILE` for MySQL before starting the container
- stdout request/application logs

Build and run it with a seeded beta account:

```bash
docker build -t crux-beta .
docker run --rm -p 8082:8082 \
  -v "$PWD/.runtime-data:/app/data" \
  -e JWT_SECRET_FILE=/run/secrets/crux-jwt-secret \
  -e AUTH_GOOGLE_CLIENT_ID_FILE=/run/secrets/crux-google-client-id \
  -e AUTH_GOOGLE_CLIENT_SECRET_FILE=/run/secrets/crux-google-client-secret \
  -e AUTH_BOOTSTRAP_USERS_FILE=/run/secrets/crux-beta-users.json \
  -v "$PWD/secrets/crux-jwt-secret:/run/secrets/crux-jwt-secret:ro" \
  -v "$PWD/secrets/crux-google-client-id:/run/secrets/crux-google-client-id:ro" \
  -v "$PWD/secrets/crux-google-client-secret:/run/secrets/crux-google-client-secret:ro" \
  -v "$PWD/secrets/crux-beta-users.json:/run/secrets/crux-beta-users.json:ro" \
  crux-beta
```

For MySQL-backed deployment, override the DB env at runtime:

```bash
docker run --rm -p 8082:8082 \
  -e DB_DIALECT=mysql \
  -e DB_DSN_FILE=/run/secrets/crux-db-dsn \
  -e JWT_SECRET_FILE=/run/secrets/crux-jwt-secret \
  -e AUTH_GOOGLE_CLIENT_ID_FILE=/run/secrets/crux-google-client-id \
  -e AUTH_GOOGLE_CLIENT_SECRET_FILE=/run/secrets/crux-google-client-secret \
  -e AUTH_BOOTSTRAP_USERS_FILE=/run/secrets/crux-beta-users.json \
  -v "$PWD/secrets/crux-db-dsn:/run/secrets/crux-db-dsn:ro" \
  -v "$PWD/secrets/crux-jwt-secret:/run/secrets/crux-jwt-secret:ro" \
  -v "$PWD/secrets/crux-google-client-id:/run/secrets/crux-google-client-id:ro" \
  -v "$PWD/secrets/crux-google-client-secret:/run/secrets/crux-google-client-secret:ro" \
  -v "$PWD/secrets/crux-beta-users.json:/run/secrets/crux-beta-users.json:ro" \
  crux-beta
```

The repository also includes `.github/workflows/beta-ci.yml`, which runs `make ci-beta`, executes both the local-mode and real `APP_MODE=prod` smoke flows, uploads the verified server binaries and logs, builds beta CLI bundles for `linux/amd64`, `darwin/amd64`, and `darwin/arm64` with matching checksum and manifest artifacts, publishes a rolling `beta` prerelease from `main`, and still creates versioned releases from tags.

For a stricter closed beta rollout, you can also restrict network access in-app:

```bash
HTTP_ALLOWED_CIDRS='203.0.113.10/32,198.51.100.0/24' \
HTTP_ADMIN_ALLOWED_CIDRS='203.0.113.10/32' \
HTTP_TRUSTED_PROXY_CIDRS='10.0.0.0/8' \
go run .
```

Use `HTTP_ADMIN_ALLOWED_CIDRS` if you want `/admin` and `/api/v1/admin/*` to be stricter than the rest of the app. If `HTTP_TRUSTED_PROXY_CIDRS` is empty, Crux only trusts the direct socket remote address and ignores forwarded IP headers.

`APP_MODE=prod` now fails fast during startup if critical closed beta settings are unsafe or incomplete, including a missing `JWT_SECRET`, invalid CIDR values, demo-user enablement, static token bypass enablement, or malformed bootstrap users.

`GET /healthz` and `GET /readyz` now also return embedded server build metadata so you can verify the exact beta revision after deploy.

## Research Agent MVP

The cloud research agent is intentionally narrow in this MVP:

- feedback report generation samples up to 10 uploaded raw queries, assistant responses, and captured reasoning summaries before sending them to the OpenAI Responses API
- the generated output is rendered as user-facing workflow feedback reports with fields such as `user_intent`, `model_interpretation`, strengths, frictions, and next steps
- the dashboard and CLI no longer create or execute local patch plans

Set `OPENAI_API_KEY` on the server process to enable live feedback report generation. The config loader maps `OPENAI_API_KEY`, `OPENAI_BASE_URL`, and `OPENAI_RESPONSES_MODEL` into the `OpenAI` config section, and `OPENAI_API_KEY_FILE` is also supported for file-based secrets.

# auto upload the release 
./scripts/build_and_publish_server_release.sh
