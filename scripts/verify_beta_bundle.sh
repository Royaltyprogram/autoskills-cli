#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_DIR="$ROOT_DIR/output/release"
ARCHIVE_PATH="${1:-${BUNDLE:-}}"

bundle_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

latest_bundle() {
  python3 - <<'PY' "$RELEASE_DIR"
import pathlib
import sys

release_dir = pathlib.Path(sys.argv[1])
archives = [p for p in release_dir.glob("crux-*.tar.gz") if p.is_file()]
if not archives:
    raise SystemExit("")
archives.sort(key=lambda p: p.stat().st_mtime, reverse=True)
print(archives[0])
PY
}

if [[ -z "$ARCHIVE_PATH" ]]; then
  ARCHIVE_PATH="$(latest_bundle)"
fi
if [[ -z "$ARCHIVE_PATH" ]]; then
  echo "no beta bundle archive found" >&2
  exit 1
fi

ARCHIVE_PATH="$(cd "$(dirname "$ARCHIVE_PATH")" && pwd)/$(basename "$ARCHIVE_PATH")"
CHECKSUM_PATH="$ARCHIVE_PATH.sha256"
MANIFEST_PATH="${ARCHIVE_PATH%.tar.gz}.json"
BUNDLE_NAME="$(basename "$ARCHIVE_PATH" .tar.gz)"

if [[ ! -f "$ARCHIVE_PATH" ]]; then
  echo "bundle archive not found: $ARCHIVE_PATH" >&2
  exit 1
fi
if [[ ! -f "$CHECKSUM_PATH" ]]; then
  echo "bundle checksum not found: $CHECKSUM_PATH" >&2
  exit 1
fi
if [[ ! -f "$MANIFEST_PATH" ]]; then
  echo "bundle manifest not found: $MANIFEST_PATH" >&2
  exit 1
fi

ACTUAL_SHA256="$(bundle_sha256 "$ARCHIVE_PATH")"
CHECKSUM_SHA256="$(awk '{print $1}' "$CHECKSUM_PATH")"
CHECKSUM_ARCHIVE_NAME="$(awk '{print $2}' "$CHECKSUM_PATH")"

if [[ "$ACTUAL_SHA256" != "$CHECKSUM_SHA256" ]]; then
  echo "checksum mismatch for $ARCHIVE_PATH" >&2
  exit 1
fi
if [[ "$CHECKSUM_ARCHIVE_NAME" != "$(basename "$ARCHIVE_PATH")" ]]; then
  echo "checksum file references unexpected archive: $CHECKSUM_ARCHIVE_NAME" >&2
  exit 1
fi

python3 - <<'PY' "$MANIFEST_PATH" "$BUNDLE_NAME" "$(basename "$ARCHIVE_PATH")" "$ACTUAL_SHA256"
import json
import sys

manifest_path, bundle_name, archive_name, actual_sha = sys.argv[1:]
with open(manifest_path, "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

required = ["bundle_name", "archive_name", "version", "commit", "build_date", "goos", "goarch", "sha256"]
for key in required:
    if not manifest.get(key):
        raise SystemExit(f"manifest missing {key}: {manifest_path}")

if manifest["bundle_name"] != bundle_name:
    raise SystemExit(f"manifest bundle_name mismatch: {manifest['bundle_name']} != {bundle_name}")
if manifest["archive_name"] != archive_name:
    raise SystemExit(f"manifest archive_name mismatch: {manifest['archive_name']} != {archive_name}")
if manifest["sha256"] != actual_sha:
    raise SystemExit(f"manifest sha256 mismatch: {manifest['sha256']} != {actual_sha}")
PY

tar_listing="$(tar -tzf "$ARCHIVE_PATH")"
required_entries=(
  "$BUNDLE_NAME/crux"
  "$BUNDLE_NAME/README.md"
)
for entry in "${required_entries[@]}"; do
  if ! grep -Fxq "$entry" <<<"$tar_listing"; then
    echo "bundle missing required entry: $entry" >&2
    exit 1
  fi
done

echo "verified beta bundle: $ARCHIVE_PATH"
