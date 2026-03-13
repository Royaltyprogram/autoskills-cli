#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_DIR="${1:-${RELEASE_DIR:-$ROOT_DIR/output/release}}"
VERSION_FILTER="${2:-${VERSION_LABEL:-}}"

bundle_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

OUTPUT_PATH="$(python3 - <<'PY' "$RELEASE_DIR" "$VERSION_FILTER"
import json
import pathlib
import sys
from datetime import datetime, timezone

release_dir = pathlib.Path(sys.argv[1])
version_filter = sys.argv[2].strip()

manifests = []
for path in release_dir.glob("crux-*.json"):
    if path.name.endswith(".release-index.json"):
        continue
    with open(path, "r", encoding="utf-8") as fh:
        manifest = json.load(fh)
    manifests.append((path, manifest))

if not manifests:
    raise SystemExit("no bundle manifests found")

if not version_filter:
    manifests.sort(key=lambda item: item[0].stat().st_mtime, reverse=True)
    version_filter = manifests[0][1]["version"]

selected = []
for path, manifest in manifests:
    if manifest.get("version") != version_filter:
        continue
    archive_path = release_dir / manifest["archive_name"]
    checksum_path = release_dir / f"{manifest['archive_name']}.sha256"
    if not archive_path.is_file():
        raise SystemExit(f"archive missing for manifest: {archive_path}")
    if not checksum_path.is_file():
        raise SystemExit(f"checksum missing for manifest: {checksum_path}")
    selected.append({
        "bundle_name": manifest["bundle_name"],
        "archive_name": manifest["archive_name"],
        "archive_path": archive_path.name,
        "checksum_path": checksum_path.name,
        "version": manifest["version"],
        "commit": manifest["commit"],
        "build_date": manifest["build_date"],
        "goos": manifest["goos"],
        "goarch": manifest["goarch"],
        "sha256": manifest["sha256"],
    })

if not selected:
    raise SystemExit(f"no bundle manifests found for version {version_filter}")

selected.sort(key=lambda item: (item["goos"], item["goarch"]))
index = {
    "version": version_filter,
    "commit": selected[0]["commit"],
    "generated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "bundles": selected,
}
output_path = release_dir / f"crux-{version_filter}.release-index.json"
with open(output_path, "w", encoding="utf-8") as fh:
    json.dump(index, fh, indent=2)
    fh.write("\n")

print(output_path)
PY
)"

INDEX_SHA256="$(bundle_sha256 "$OUTPUT_PATH")"
printf "%s  %s\n" "$INDEX_SHA256" "$(basename "$OUTPUT_PATH")" > "$OUTPUT_PATH.sha256"

echo "$OUTPUT_PATH"
