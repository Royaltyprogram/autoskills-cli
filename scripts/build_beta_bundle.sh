#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOOS_VALUE="${GOOS:-$(go env GOOS)}"
GOARCH_VALUE="${GOARCH:-$(go env GOARCH)}"
VERSION_LABEL="${VERSION_LABEL:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo beta)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"
GIT_COMMIT="${GIT_COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUNDLE_NAME="agentopt-${VERSION_LABEL}-${GOOS_VALUE}-${GOARCH_VALUE}"
RELEASE_DIR="$ROOT_DIR/output/release"
STAGE_DIR="$RELEASE_DIR/$BUNDLE_NAME"
ARCHIVE_PATH="$RELEASE_DIR/$BUNDLE_NAME.tar.gz"
RUNNER_DIR="$ROOT_DIR/tools/codex-runner"

mkdir -p "$RELEASE_DIR"
rm -rf "$STAGE_DIR" "$ARCHIVE_PATH"
mkdir -p "$STAGE_DIR/tools/codex-runner"

if [[ ! -d "$RUNNER_DIR/node_modules" ]]; then
  (cd "$RUNNER_DIR" && npm ci --omit=dev)
fi

(cd "$ROOT_DIR" && GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" go build \
  -ldflags "-X github.com/liushuangls/go-server-template/pkg/buildinfo.Version=$VERSION_LABEL -X github.com/liushuangls/go-server-template/pkg/buildinfo.Commit=$GIT_COMMIT -X github.com/liushuangls/go-server-template/pkg/buildinfo.Date=$BUILD_DATE" \
  -o "$STAGE_DIR/agentopt" ./cmd/agentopt)

cp "$RUNNER_DIR/run.mjs" "$STAGE_DIR/tools/codex-runner/"
cp "$RUNNER_DIR/package.json" "$STAGE_DIR/tools/codex-runner/"
cp "$RUNNER_DIR/package-lock.json" "$STAGE_DIR/tools/codex-runner/"
cp -R "$RUNNER_DIR/node_modules" "$STAGE_DIR/tools/codex-runner/"
sed \
  -e "s/__VERSION__/$VERSION_LABEL/g" \
  -e "s/__COMMIT__/$GIT_COMMIT/g" \
  -e "s/__BUILD_DATE__/$BUILD_DATE/g" \
  "$ROOT_DIR/docs/beta-cli-bundle.md" > "$STAGE_DIR/README.md"

tar -C "$RELEASE_DIR" -czf "$ARCHIVE_PATH" "$BUNDLE_NAME"

echo "$ARCHIVE_PATH"
