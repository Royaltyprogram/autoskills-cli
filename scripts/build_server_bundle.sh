#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOOS_VALUE="${GOOS:-$(go env GOOS)}"
GOARCH_VALUE="${GOARCH:-$(go env GOARCH)}"
VERSION_LABEL="${VERSION_LABEL:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo beta)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"
GIT_COMMIT="${GIT_COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUNDLE_NAME="crux-server-${VERSION_LABEL}-${GOOS_VALUE}-${GOARCH_VALUE}"
RELEASE_DIR="${RELEASE_DIR:-$ROOT_DIR/output/release}"
STAGE_DIR="$RELEASE_DIR/$BUNDLE_NAME"
ARCHIVE_PATH="$RELEASE_DIR/$BUNDLE_NAME.tar.gz"
CHECKSUM_PATH="$ARCHIVE_PATH.sha256"
MANIFEST_PATH="$RELEASE_DIR/$BUNDLE_NAME.json"

mkdir -p "$RELEASE_DIR"
rm -rf "$STAGE_DIR" "$ARCHIVE_PATH" "$CHECKSUM_PATH" "$MANIFEST_PATH"
mkdir -p "$STAGE_DIR"

(cd "$ROOT_DIR" && GOOS="$GOOS_VALUE" GOARCH="$GOARCH_VALUE" go build \
  -ldflags "-X github.com/Royaltyprogram/aiops/pkg/buildinfo.Version=$VERSION_LABEL -X github.com/Royaltyprogram/aiops/pkg/buildinfo.Commit=$GIT_COMMIT -X github.com/Royaltyprogram/aiops/pkg/buildinfo.Date=$BUILD_DATE" \
  -o "$STAGE_DIR/server" main.go wire_gen.go)

cat > "$STAGE_DIR/README.md" <<EOF
# Crux Server $VERSION_LABEL

This archive contains the prebuilt Crux backend server for $GOOS_VALUE/$GOARCH_VALUE.

Quick start:

\`\`\`bash
APP_MODE=prod ./server
\`\`\`

The server reads runtime configuration from the same environment variables and secret files documented in the repository README.
EOF

tar -C "$RELEASE_DIR" -czf "$ARCHIVE_PATH" "$BUNDLE_NAME"

bundle_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

ARCHIVE_SHA256="$(bundle_sha256 "$ARCHIVE_PATH")"
printf "%s  %s\n" "$ARCHIVE_SHA256" "$(basename "$ARCHIVE_PATH")" > "$CHECKSUM_PATH"
cat > "$MANIFEST_PATH" <<EOF
{
  "bundle_name": "$BUNDLE_NAME",
  "archive_name": "$(basename "$ARCHIVE_PATH")",
  "binary_name": "server",
  "version": "$VERSION_LABEL",
  "commit": "$GIT_COMMIT",
  "build_date": "$BUILD_DATE",
  "goos": "$GOOS_VALUE",
  "goarch": "$GOARCH_VALUE",
  "sha256": "$ARCHIVE_SHA256"
}
EOF

echo "$ARCHIVE_PATH"
