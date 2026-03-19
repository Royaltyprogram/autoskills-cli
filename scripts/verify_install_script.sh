#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_DIR="${RELEASE_DIR:-$ROOT_DIR/output/release}"
BUNDLE_PATH="${1:-${BUNDLE:-}}"

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}

sha256_file() {
  python3 - <<'PY' "$1"
import hashlib
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
digest = hashlib.sha256(path.read_bytes()).hexdigest()
print(digest)
PY
}

node_platform() {
  case "$(uname -s)" in
    Darwin) printf 'darwin\n' ;;
    Linux) printf 'linux\n' ;;
    *)
      echo "unsupported operating system for fake node dist: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

node_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'x64\n' ;;
    arm64|aarch64) printf 'arm64\n' ;;
    *)
      echo "unsupported architecture for fake node dist: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

latest_bundle() {
  python3 - <<'PY' "$RELEASE_DIR"
import pathlib
import sys

release_dir = pathlib.Path(sys.argv[1])
archives = [p for p in release_dir.glob("autoskills-*.tar.gz") if p.is_file()]
if not archives:
    raise SystemExit("")
archives.sort(key=lambda p: p.stat().st_mtime, reverse=True)
print(archives[0])
PY
}

bundle_version() {
  local bundle_name
  bundle_name="$(basename "$1" .tar.gz)"
  python3 - <<'PY' "$bundle_name"
import sys

name = sys.argv[1]
prefix = "autoskills-"
if not name.startswith(prefix):
    raise SystemExit("")
raw = name[len(prefix):]
parts = raw.rsplit("-", 2)
if len(parts) != 3:
    raise SystemExit("")
print(parts[0])
PY
}

need_cmd python3
need_cmd tar
need_cmd sh

if [[ -z "$BUNDLE_PATH" ]]; then
  BUNDLE_PATH="$(latest_bundle)"
fi
if [[ -z "$BUNDLE_PATH" ]]; then
  echo "no beta bundle archive found" >&2
  exit 1
fi

BUNDLE_PATH="$(cd "$(dirname "$BUNDLE_PATH")" && pwd)/$(basename "$BUNDLE_PATH")"
[[ -f "$BUNDLE_PATH" ]] || {
  echo "bundle archive not found: $BUNDLE_PATH" >&2
  exit 1
}

VERSION_LABEL="${VERSION_LABEL:-$(bundle_version "$BUNDLE_PATH")}"
[[ -n "$VERSION_LABEL" ]] || {
  echo "failed to infer bundle version from $BUNDLE_PATH" >&2
  exit 1
}

CHECKSUM_PATH="$BUNDLE_PATH.sha256"
[[ -f "$CHECKSUM_PATH" ]] || {
  echo "bundle checksum not found: $CHECKSUM_PATH" >&2
  exit 1
}

TMPDIR_WORK="$(mktemp -d "${TMPDIR:-/tmp}/autoskills-install-verify.XXXXXX")"
cleanup() {
  rm -rf "$TMPDIR_WORK"
}
trap cleanup EXIT

STAGED_RELEASE_DIR="$TMPDIR_WORK/releases/$VERSION_LABEL"
INSTALL_ROOT="$TMPDIR_WORK/install-root"
BIN_DIR="$TMPDIR_WORK/bin"
DEFAULT_HOME="$TMPDIR_WORK/home-default"
mkdir -p "$STAGED_RELEASE_DIR" "$INSTALL_ROOT" "$BIN_DIR" "$DEFAULT_HOME"

cp "$BUNDLE_PATH" "$STAGED_RELEASE_DIR/"
cp "$CHECKSUM_PATH" "$STAGED_RELEASE_DIR/"

HOME="$DEFAULT_HOME" \
AUTOSKILLS_VERSION="$VERSION_LABEL" \
AUTOSKILLS_RELEASE_BASE_URL="file://$TMPDIR_WORK/releases" \
AUTOSKILLS_INSTALL_ROOT="$INSTALL_ROOT" \
AUTOSKILLS_BIN_DIR="$BIN_DIR" \
AUTOSKILLS_AUTO_PATH=never \
AUTOSKILLS_INSTALL_NODE=never \
sh "$ROOT_DIR/scripts/install.sh" >/dev/null

[[ -x "$BIN_DIR/autoskills" ]] || {
  echo "install script did not create wrapper: $BIN_DIR/autoskills" >&2
  exit 1
}

VERSION_OUTPUT="$("$BIN_DIR/autoskills" version)"
[[ "$VERSION_OUTPUT" == autoskills\ "$VERSION_LABEL"* ]] || {
  echo "unexpected version output: $VERSION_OUTPUT" >&2
  exit 1
}

[[ -L "$INSTALL_ROOT/current" ]] || {
  echo "install script did not create current symlink" >&2
  exit 1
}

# Re-run install to verify idempotent upgrade behavior for the same version.
HOME="$DEFAULT_HOME" \
AUTOSKILLS_VERSION="$VERSION_LABEL" \
AUTOSKILLS_RELEASE_BASE_URL="file://$TMPDIR_WORK/releases" \
AUTOSKILLS_INSTALL_ROOT="$INSTALL_ROOT" \
AUTOSKILLS_BIN_DIR="$BIN_DIR" \
AUTOSKILLS_AUTO_PATH=never \
AUTOSKILLS_INSTALL_NODE=never \
sh "$ROOT_DIR/scripts/install.sh" >/dev/null

VERSION_OUTPUT="$("$BIN_DIR/autoskills" version)"
[[ "$VERSION_OUTPUT" == autoskills\ "$VERSION_LABEL"* ]] || {
  echo "unexpected version output after reinstall: $VERSION_OUTPUT" >&2
  exit 1
}

MIGRATE_HOME="$TMPDIR_WORK/home-with-codex"
MIGRATE_INSTALL_ROOT="$TMPDIR_WORK/install-root-with-codex"
MIGRATE_BIN_DIR="$TMPDIR_WORK/bin-with-codex"
mkdir -p "$MIGRATE_HOME/.codex" "$MIGRATE_INSTALL_ROOT" "$MIGRATE_BIN_DIR"
cat >"$MIGRATE_HOME/.codex/AGENTS.md" <<'EOF'
<!-- autoskills-personal-skillset:start -->
## AutoSkills Personal Skillset

Before processing any query, you MUST first review and apply the user's personal skillset located at:

`skills/autoskills-personal-skillset/SKILL.md`

**Workflow:**
1. Load `skills/autoskills-personal-skillset/SKILL.md` at the start of every conversation.
2. Apply only the category documents relevant to the current request.
3. Preserve these rules across follow-up turns unless a newer rule overrides them.
<!-- autoskills-personal-skillset:end -->
EOF

HOME="$MIGRATE_HOME" \
AUTOSKILLS_VERSION="$VERSION_LABEL" \
AUTOSKILLS_RELEASE_BASE_URL="file://$TMPDIR_WORK/releases" \
AUTOSKILLS_INSTALL_ROOT="$MIGRATE_INSTALL_ROOT" \
AUTOSKILLS_BIN_DIR="$MIGRATE_BIN_DIR" \
AUTOSKILLS_AUTO_PATH=never \
AUTOSKILLS_INSTALL_NODE=never \
sh "$ROOT_DIR/scripts/install.sh" >/dev/null

grep -F 'use `$autoskills-personal-skillset` as the user' "$MIGRATE_HOME/.codex/AGENTS.md" >/dev/null || {
  echo "install script did not update Codex AGENTS.md managed section" >&2
  exit 1
}

grep -F '$CODEX_HOME/skills/autoskills-personal-skillset/SKILL.md' "$MIGRATE_HOME/.codex/AGENTS.md" >/dev/null || {
  echo "install script did not write the new Codex skill entry path" >&2
  exit 1
}

if grep -F '`skills/autoskills-personal-skillset/SKILL.md`' "$MIGRATE_HOME/.codex/AGENTS.md" >/dev/null; then
  echo "install script left the legacy Codex AGENTS.md instruction in place" >&2
  exit 1
fi

NODE_VERSION_TAG="v20.11.1"
NODE_PLATFORM="$(node_platform)"
NODE_ARCH="$(node_arch)"
NODE_DIST_ROOT="$TMPDIR_WORK/node-dist"
NODE_STAGE_ROOT="$TMPDIR_WORK/node-stage"
NODE_ARCHIVE_BASE="node-$NODE_VERSION_TAG-$NODE_PLATFORM-$NODE_ARCH"
NODE_ARCHIVE_PATH="$NODE_DIST_ROOT/$NODE_VERSION_TAG/$NODE_ARCHIVE_BASE.tar.gz"
NODE_CHECKSUM_PATH="$NODE_DIST_ROOT/$NODE_VERSION_TAG/SHASUMS256.txt"
FORCED_INSTALL_ROOT="$TMPDIR_WORK/install-root-with-node"
FORCED_BIN_DIR="$TMPDIR_WORK/bin-with-node"

mkdir -p "$NODE_STAGE_ROOT/$NODE_ARCHIVE_BASE/bin" "$NODE_DIST_ROOT/$NODE_VERSION_TAG" "$FORCED_INSTALL_ROOT" "$FORCED_BIN_DIR"
cat >"$NODE_STAGE_ROOT/$NODE_ARCHIVE_BASE/bin/node" <<EOF
#!/bin/sh
if [ "\${1:-}" = "--version" ]; then
  printf '%s\n' "$NODE_VERSION_TAG"
  exit 0
fi
printf 'fake node invoked\n'
EOF
chmod 755 "$NODE_STAGE_ROOT/$NODE_ARCHIVE_BASE/bin/node"
tar -czf "$NODE_ARCHIVE_PATH" -C "$NODE_STAGE_ROOT" "$NODE_ARCHIVE_BASE"
printf '%s  %s\n' "$(sha256_file "$NODE_ARCHIVE_PATH")" "$(basename "$NODE_ARCHIVE_PATH")" >"$NODE_CHECKSUM_PATH"

HOME="$DEFAULT_HOME" \
AUTOSKILLS_VERSION="$VERSION_LABEL" \
AUTOSKILLS_RELEASE_BASE_URL="file://$TMPDIR_WORK/releases" \
AUTOSKILLS_INSTALL_ROOT="$FORCED_INSTALL_ROOT" \
AUTOSKILLS_BIN_DIR="$FORCED_BIN_DIR" \
AUTOSKILLS_AUTO_PATH=never \
AUTOSKILLS_INSTALL_NODE=always \
AUTOSKILLS_NODE_VERSION="$NODE_VERSION_TAG" \
AUTOSKILLS_NODE_DIST_BASE_URL="file://$NODE_DIST_ROOT" \
sh "$ROOT_DIR/scripts/install.sh" >/dev/null

[[ -x "$FORCED_INSTALL_ROOT/node/current/bin/node" ]] || {
  echo "install script did not provision local node runtime" >&2
  exit 1
}

NODE_VERSION_OUTPUT="$("$FORCED_INSTALL_ROOT/node/current/bin/node" --version)"
[[ "$NODE_VERSION_OUTPUT" == "$NODE_VERSION_TAG" ]] || {
  echo "unexpected local node version output: $NODE_VERSION_OUTPUT" >&2
  exit 1
}

grep -F "$FORCED_INSTALL_ROOT/node/current/bin" "$FORCED_BIN_DIR/autoskills" >/dev/null || {
  echo "autoskills wrapper does not include local node path" >&2
  exit 1
}

AUTO_HOME="$TMPDIR_WORK/home-zsh"
AUTO_LOG="$TMPDIR_WORK/install-auto-path.log"
AUTO_BIN_DIR="$AUTO_HOME/.local/bin"
AUTO_INSTALL_ROOT="$AUTO_HOME/.local/share/autoskills"
mkdir -p "$AUTO_HOME"

HOME="$AUTO_HOME" \
SHELL="/bin/zsh" \
PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
AUTOSKILLS_VERSION="$VERSION_LABEL" \
AUTOSKILLS_RELEASE_BASE_URL="file://$TMPDIR_WORK/releases" \
AUTOSKILLS_INSTALL_ROOT="$AUTO_INSTALL_ROOT" \
AUTOSKILLS_INSTALL_NODE=never \
sh "$ROOT_DIR/scripts/install.sh" >"$AUTO_LOG" 2>&1

[[ -x "$AUTO_BIN_DIR/autoskills" ]] || {
  echo "install script did not create default wrapper under HOME: $AUTO_BIN_DIR/autoskills" >&2
  exit 1
}

for profile in "$AUTO_HOME/.zprofile" "$AUTO_HOME/.zshrc"; do
  [[ -f "$profile" ]] || {
    echo "install script did not create shell profile: $profile" >&2
    exit 1
  }
  grep -F 'Added by autoskills installer' "$profile" >/dev/null || {
    echo "install script did not add autoskills PATH marker to $profile" >&2
    exit 1
  }
  grep -F '$HOME/.local/bin' "$profile" >/dev/null || {
    echo "install script did not add default HOME bin path to $profile" >&2
    exit 1
  }
done

grep -F 'restart your shell or run: export PATH="' "$AUTO_LOG" >/dev/null || {
  echo "install script did not print shell reload guidance after auto PATH setup" >&2
  exit 1
}

HOME="$AUTO_HOME" \
SHELL="/bin/zsh" \
PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
AUTOSKILLS_VERSION="$VERSION_LABEL" \
AUTOSKILLS_RELEASE_BASE_URL="file://$TMPDIR_WORK/releases" \
AUTOSKILLS_INSTALL_ROOT="$AUTO_INSTALL_ROOT" \
AUTOSKILLS_INSTALL_NODE=never \
sh "$ROOT_DIR/scripts/install.sh" >/dev/null 2>&1

for profile in "$AUTO_HOME/.zprofile" "$AUTO_HOME/.zshrc"; do
  count="$(grep -c 'Added by autoskills installer' "$profile")"
  [[ "$count" -eq 1 ]] || {
    echo "install script appended duplicate PATH snippet to $profile" >&2
    exit 1
  }
done

echo "verified install script: $BUNDLE_PATH"
