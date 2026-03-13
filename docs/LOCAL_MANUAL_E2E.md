# Local Manual E2E

This document is the shortest manual path for validating the current report-based workflow:

- local agent usage is uploaded
- the server generates workflow feedback reports
- the dashboard and CLI surface those reports

## Scope

Assumptions:

- repo root is the current working directory
- Python env exists at `myenv/`
- you will use a real OpenAI API key if you want live report generation
- you will create at least one real Codex session before the first upload

## 1. Start Clean

```bash
rm -rf .agentopt-dev .agentopt-live-daemon .agentopt-live-test .codex
rm -f data/agentopt.db data/agentopt-local.db data/agentopt-store.json
find secrets -maxdepth 1 -type f ! -name '.gitignore' ! -name '*.example' -delete
rm -rf ~/.agentopt
```

Do not remove `~/.codex/sessions` if you still need older real Codex sessions for upload.

## 2. Recreate Local Secrets

```bash
cp secrets/agentopt-jwt-secret.example secrets/agentopt-jwt-secret
cp secrets/agentopt-beta-users.json.example secrets/agentopt-beta-users.json
cp secrets/agentopt-openai-api-key.example secrets/agentopt-openai-api-key
```

Required contents:

- `secrets/agentopt-jwt-secret`: one strong random string
- `secrets/agentopt-beta-users.json`: at least one beta user entry
- `secrets/agentopt-openai-api-key`: one real OpenAI API key if you want report generation

## 3. Start The Server

```bash
source myenv/bin/activate
APP_MODE=prod \
JWT_SECRET_FILE=secrets/agentopt-jwt-secret \
AUTH_BOOTSTRAP_USERS_FILE=secrets/agentopt-beta-users.json \
OPENAI_API_KEY_FILE=secrets/agentopt-openai-api-key \
DB_DSN='data/agentopt.db?_fk=1' \
DB_DIALECT=sqlite3 \
go run .
```

Keep this shell open.

## 4. Log In On The Web

Open `http://127.0.0.1:8082/`.

Sign in with the user you put into `secrets/agentopt-beta-users.json`.

Issue a CLI token from the dashboard.

## 5. Setup The Local CLI

In another shell:

```bash
source myenv/bin/activate
go run ./cmd/agentopt setup --server http://127.0.0.1:8082 --token <CLI_TOKEN_FROM_DASHBOARD>
go run ./cmd/agentopt workspace
```

## 6. Upload A Snapshot

```bash
source myenv/bin/activate
go run ./cmd/agentopt snapshot --file examples/config-snapshot.json
```

## 7. Generate Real Local Usage Data

Create a real Codex session in this repo. The session should contain real raw queries, not a hand-written summary JSON.

Good seed prompts:

- `Inspect the route handler and summarize the current control flow.`
- `Find the smallest patch that fixes the failing analytics path.`
- `List the exact tests to run after the patch.`

After that session exists under `~/.codex/sessions`, upload it:

```bash
source myenv/bin/activate
go run ./cmd/agentopt collect --codex-home ~/.codex --recent 1 --snapshot-mode changed
go run ./cmd/agentopt sessions --limit 5
go run ./cmd/agentopt reports
```

If the server is configured with a research model and enough sessions have been uploaded, you should now see workflow feedback reports in the CLI and dashboard.
Those reports should now include `user_intent` and `model_interpretation`, and recent sessions may show captured reasoning summaries when the local Codex session contains them.

## 8. Verify The Report Surface

Recommended places to inspect:

- dashboard overview and latest report cards
- `go run ./cmd/agentopt reports`
- `go run ./cmd/agentopt status`
- `go run ./cmd/agentopt audit`

Expected report fields now include:

- `title`
- `summary`
- `confidence`
- `strengths`
- `frictions`
- `next_steps`

Nothing should be auto-applied back into the local agent.

## 9. Upload Follow-Up Sessions

Create one or more more real Codex sessions after reading the first report, then upload them:

```bash
source myenv/bin/activate
go run ./cmd/agentopt collect --codex-home ~/.codex --recent 2 --snapshot-mode skip
go run ./cmd/agentopt reports
go run ./cmd/agentopt status
```

The next report refresh should reflect the newer usage pattern once the background research pass completes.

## 10. Optional Watch Mode

To keep usage uploads flowing during a manual session:

```bash
source myenv/bin/activate
go run ./cmd/agentopt collect --watch --recent 1 --interval 30m
```

## 11. Clean Up After The Run

```bash
rm -rf .agentopt-dev .agentopt-live-daemon .agentopt-live-test .codex
rm -f data/agentopt.db data/agentopt-local.db data/agentopt-store.json
find secrets -maxdepth 1 -type f ! -name '.gitignore' ! -name '*.example' -delete
rm -rf ~/.agentopt
```
