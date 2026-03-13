# Closed Beta Runbook

## Scope

This runbook is the shortest path from local verification to a release-candidate bundle for the current closed beta build.

It assumes:

- the repo root is your working directory
- Go, `wire`, and the local toolchain are already installed
- the server runs on `http://127.0.0.1:8082` by default

For beta user machines that install the released CLI, Go is not required. Only the development-from-source flow below uses `go run`.

## 1. Local Development Run

Start the local development server:

```bash
make generate
OPENAI_API_KEY_FILE=secrets/crux-openai-api-key make run
```

Open `http://127.0.0.1:8082/` and sign in with the local demo account:

- email: `demo@example.com`
- password: `demo1234`

From another shell, run the CLI against the local server. These commands are for source development:

```bash
go run ./cmd/crux setup --server http://127.0.0.1:8082 --token <CLI_TOKEN_FROM_DASHBOARD>
go run ./cmd/crux workspace
go run ./cmd/crux snapshot --file examples/config-snapshot.json
go run ./cmd/crux session
go run ./cmd/crux collect --watch --recent 1 --interval 30m
go run ./cmd/crux reports
go run ./cmd/crux audit
```

Notes:

- In the MVP, every connected repository rolls into one shared workspace per organization.
- `crux setup` is the shortest onboarding path and includes the initial workspace connection automatically.
- installed macOS beta machines also get background collection automatically when setup can register a launchd agent
- `crux connect` remains available when you need to reconnect a different repo manually.
- `crux collect --watch` keeps session and snapshot uploads flowing while the shared workspace is being observed.
- Reports are now read-only feedback reports for the user; nothing is auto-applied.

If you want a beta machine to keep uploading usage data without repeated manual CLI runs, keep a long-lived collector running:

```bash
crux collect --watch --recent 1 --interval 30m
```

Notes:

- Prefer the installed `crux` command for long-lived beta machine setup. On supported installed macOS environments, `crux setup --server ...` now enrolls that background collector automatically.
- Keep `crux collect` without `--watch` for one-off manual uploads.

For beta users who should install from GitHub Releases instead of an unpacked bundle:

```bash
curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/aiops/main/scripts/install.sh | sh
CRUX_VERSION=0.1.0-beta.1 curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/aiops/main/scripts/install.sh | sh
```

The installer downloads the matching release bundle, installs it under `~/.local/share/crux/<version>`, and writes a wrapper to `~/.local/bin/crux`.
That release install uses a prebuilt binary, so Go is not required on the beta machine.

## 2. Local Verification

Run the fast confidence checks:

```bash
go test ./...
```

Run the closed beta smoke with a seeded beta user:

```bash
export BETA_SMOKE_EMAIL=beta1@example.com
export BETA_SMOKE_PASSWORD=replace-me
make closed-beta-smoke
```

Run the real `APP_MODE=prod` smoke locally:

```bash
make closed-beta-prod-smoke
```

Run the same smoke against the ignored local secret files under `secrets/` and require the research agent to use the live OpenAI path:

```bash
JWT_SECRET_FILE_OVERRIDE=secrets/crux-jwt-secret \
AUTH_BOOTSTRAP_USERS_FILE_OVERRIDE=secrets/crux-beta-users.json \
OPENAI_API_KEY_FILE_OVERRIDE=secrets/crux-openai-api-key \
EXPECT_RESEARCH_MODE=openai_responses_api \
make closed-beta-prod-smoke
```

Run the full beta CI-equivalent path locally:

```bash
make ci-beta
```

## 3. Runtime Secrets

For closed beta or production, do not use the local demo path.

Preferred runtime secret inputs:

- `JWT_SECRET_FILE`
- `AUTH_BOOTSTRAP_USERS_FILE`
- `DB_DSN_FILE` if using MySQL
- `OPENAI_API_KEY` or `OPENAI_API_KEY_FILE` for the cloud research agent
- `HTTP_ALLOWED_CIDRS`
- `HTTP_ADMIN_ALLOWED_CIDRS`
- `HTTP_TRUSTED_PROXY_CIDRS`

Supported file-based secret envs:

- `JWT_SECRET_FILE`
- `OPENAI_API_KEY_FILE`
- `DB_DSN_FILE`
- `APP_API_TOKEN_FILE`
- `AUTH_BOOTSTRAP_USERS_FILE`

Bootstrap users are managed identities:

- removing a bootstrap user revokes existing tokens
- rotating a bootstrap password revokes prior sessions
- seed at least one bootstrap user with `"role":"admin"` if you need the admin user-management APIs; omitted roles default to `member`

## 4. Local Prod-Like Run

Run the server in `prod` mode with secret files:

```bash
APP_MODE=prod \
JWT_SECRET_FILE=/run/secrets/crux-jwt-secret \
AUTH_BOOTSTRAP_USERS_FILE=/run/secrets/crux-beta-users.json \
OPENAI_API_KEY_FILE=/run/secrets/crux-openai-api-key \
go run .
```

Health endpoints:

- `GET /healthz`
- `GET /readyz`

If you want to lock access down in-app during closed beta:

```bash
APP_MODE=prod \
JWT_SECRET_FILE=/run/secrets/crux-jwt-secret \
AUTH_BOOTSTRAP_USERS_FILE=/run/secrets/crux-beta-users.json \
HTTP_ALLOWED_CIDRS='203.0.113.10/32,198.51.100.0/24' \
HTTP_ADMIN_ALLOWED_CIDRS='203.0.113.10/32' \
HTTP_TRUSTED_PROXY_CIDRS='10.0.0.0/8' \
go run .
```

## 5. Container Run

Build the container:

```bash
docker build -t crux-beta .
```

Run with SQLite-backed beta state:

```bash
docker run --rm -p 8082:8082 \
  -v "$PWD/.runtime-data:/app/data" \
  -e JWT_SECRET_FILE=/run/secrets/crux-jwt-secret \
  -e AUTH_BOOTSTRAP_USERS_FILE=/run/secrets/crux-beta-users.json \
  -v "$PWD/secrets/crux-jwt-secret:/run/secrets/crux-jwt-secret:ro" \
  -v "$PWD/secrets/crux-beta-users.json:/run/secrets/crux-beta-users.json:ro" \
  crux-beta
```

Run with MySQL-backed beta state:

```bash
docker run --rm -p 8082:8082 \
  -e DB_DIALECT=mysql \
  -e DB_DSN_FILE=/run/secrets/crux-db-dsn \
  -e JWT_SECRET_FILE=/run/secrets/crux-jwt-secret \
  -e AUTH_BOOTSTRAP_USERS_FILE=/run/secrets/crux-beta-users.json \
  -v "$PWD/secrets/crux-db-dsn:/run/secrets/crux-db-dsn:ro" \
  -v "$PWD/secrets/crux-jwt-secret:/run/secrets/crux-jwt-secret:ro" \
  -v "$PWD/secrets/crux-beta-users.json:/run/secrets/crux-beta-users.json:ro" \
  crux-beta
```

## 6. Release Candidate Build

If your working tree is clean, build the release artifacts directly:

```bash
make beta-cli-bundle
make verify-beta-bundle
make verify-install-script
make build-release-index
VERSION_LABEL=0.1.0-beta.1 make publish-github-release
```

Output:

- `output/release/crux-<version>-<os>-<arch>.tar.gz`
- `output/release/crux-<version>-<os>-<arch>.tar.gz.sha256`
- `output/release/crux-<version>-<os>-<arch>.json`
- `output/release/crux-<version>.release-index.json`
- `output/release/crux-<version>.release-index.json.sha256`

`publish-github-release` expects the `gh` CLI to be authenticated and uploads those assets to the matching GitHub Release tag.

The GitHub Actions beta workflow is also wired to publish the release automatically on tag pushes.

For manual GitHub Actions runs, `workflow_dispatch` now accepts `version`, `draft`, `prerelease`, and `latest` inputs so you can build and publish the same release flow without pushing the tag first.

If your working tree is dirty but you want a clean release candidate from `HEAD`, use a temporary worktree:

```bash
tmpdir=$(mktemp -d /tmp/crux-release.XXXXXX)
git worktree add --detach "$tmpdir" HEAD
cd "$tmpdir"
make closed-beta-prod-smoke
make beta-cli-bundle
make verify-beta-bundle
make build-release-index
```

## 7. Runtime Store Backup

Export the live runtime store:

```bash
make store-export OUTPUT=output/runtime-store-backup.json
```

Restore the live runtime store:

```bash
make store-import INPUT=output/runtime-store-backup.json
```

`store-import` is destructive by design and is gated behind `--yes` internally.

## 8. Final Pre-Deploy Checklist

Run this before handing the build to beta users:

1. `git status --short` shows only expected files.
2. `go test ./...` passes.
3. The dashboard shows workflow feedback reports instead of approval actions.
4. `make closed-beta-prod-smoke` passes.
5. `make beta-cli-bundle`, `make verify-beta-bundle`, and `make verify-install-script` pass.
6. The release bundle contains `crux` and the generated `README.md`.
7. Runtime secrets are mounted from files, not hardcoded.
8. `GET /healthz` and `GET /readyz` respond successfully in the target environment.
9. A seeded beta user can log in on the dashboard and issue a CLI token.
10. A beta machine can complete `crux setup` and `crux collect`.
11. If background collection is part of the beta flow, `crux collect --watch --recent 1 --interval 30m` succeeds on the target machine.
