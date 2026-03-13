#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SMOKE_HOST="${SMOKE_HOST:-127.0.0.1}"
SMOKE_PORT="${SMOKE_PORT:-18082}"
BASE_URL="${BASE_URL:-http://${SMOKE_HOST}:${SMOKE_PORT}}"
SERVER_ADDR="${SERVER_ADDR:-:${SMOKE_PORT}}"
SERVER_BIN="${SERVER_BIN:-$ROOT_DIR/output/server}"
CLI_BIN="${CLI_BIN:-$ROOT_DIR/output/crux}"
EMAIL="${BETA_SMOKE_EMAIL:-beta1@example.com}"
PASSWORD="${BETA_SMOKE_PASSWORD:-replace-me}"
JWT_SECRET_VALUE="${JWT_SECRET_VALUE:-closed-beta-prod-smoke-secret}"
JWT_SECRET_FILE_OVERRIDE="${JWT_SECRET_FILE_OVERRIDE:-}"
AUTH_BOOTSTRAP_USERS_FILE_OVERRIDE="${AUTH_BOOTSTRAP_USERS_FILE_OVERRIDE:-}"
OPENAI_API_KEY_FILE_OVERRIDE="${OPENAI_API_KEY_FILE_OVERRIDE:-}"
EXPECT_RESEARCH_MODE="${EXPECT_RESEARCH_MODE:-}"
KEEP_RUNTIME="${KEEP_RUNTIME:-false}"
RUNTIME_DIR="$(mktemp -d)"
DATA_DIR="$RUNTIME_DIR/data"
SECRET_DIR="$RUNTIME_DIR/secrets"
SERVER_LOG_PATH="${SERVER_LOG_PATH:-$RUNTIME_DIR/server.log}"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  if [[ "$KEEP_RUNTIME" != "true" ]]; then
    rm -rf "$RUNTIME_DIR"
  fi
}
trap cleanup EXIT

mkdir -p "$DATA_DIR" "$SECRET_DIR"

printf "%s\n" "$JWT_SECRET_VALUE" > "$SECRET_DIR/jwt-secret"
printf "%s\n" "$DATA_DIR/crux-prod.db?_fk=1" > "$SECRET_DIR/db-dsn"
cat > "$SECRET_DIR/bootstrap-users.json" <<EOF
[
  {
    "id": "beta-user-1",
    "org_id": "beta-org",
    "org_name": "Beta Org",
    "email": "$EMAIL",
    "name": "Beta Operator",
    "password": "$PASSWORD"
  }
]
EOF

JWT_SECRET_FILE_PATH="$SECRET_DIR/jwt-secret"
AUTH_BOOTSTRAP_USERS_FILE_PATH="$SECRET_DIR/bootstrap-users.json"
if [[ -n "$JWT_SECRET_FILE_OVERRIDE" ]]; then
  JWT_SECRET_FILE_PATH="$JWT_SECRET_FILE_OVERRIDE"
fi
if [[ -n "$AUTH_BOOTSTRAP_USERS_FILE_OVERRIDE" ]]; then
  AUTH_BOOTSTRAP_USERS_FILE_PATH="$AUTH_BOOTSTRAP_USERS_FILE_OVERRIDE"
fi

if [[ ! -x "$SERVER_BIN" || ! -x "$CLI_BIN" ]]; then
  (cd "$ROOT_DIR" && make build)
fi

APP_MODE=prod \
APP_ADDR="$SERVER_ADDR" \
JWT_SECRET_FILE="$JWT_SECRET_FILE_PATH" \
DB_DSN_FILE="$SECRET_DIR/db-dsn" \
AUTH_BOOTSTRAP_USERS_FILE="$AUTH_BOOTSTRAP_USERS_FILE_PATH" \
OPENAI_API_KEY_FILE="$OPENAI_API_KEY_FILE_OVERRIDE" \
HTTP_LOG_TO_STDOUT=true \
"$SERVER_BIN" >"$SERVER_LOG_PATH" 2>&1 &
SERVER_PID=$!

for attempt in $(seq 1 30); do
  if curl -fsS "$BASE_URL/healthz" >/dev/null && curl -fsS "$BASE_URL/readyz" >/dev/null; then
    BETA_SMOKE_EMAIL="$EMAIL" \
    BETA_SMOKE_PASSWORD="$PASSWORD" \
    BASE_URL="$BASE_URL" \
    CLI_BIN="$CLI_BIN" \
    EXPECT_RESEARCH_MODE="$EXPECT_RESEARCH_MODE" \
    "$ROOT_DIR/scripts/closed_beta_smoke.sh"
    echo "closed beta prod smoke passed"
    exit 0
  fi
  sleep 1
done

cat "$SERVER_LOG_PATH" >&2
exit 1
