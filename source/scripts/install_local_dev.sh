#!/bin/sh

set -eu

usage() {
  cat <<'EOF'
Install the current repository's autoskills CLI into the local wrapper location.

Usage:
  ./scripts/install_local_dev.sh [--install-root <dir>] [--bin-dir <dir>] [--label <name>]

Environment overrides:
  AUTOSKILLS_INSTALL_ROOT   install root (default: $HOME/.local/share/autoskills)
  AUTOSKILLS_BIN_DIR        wrapper script directory (default: $HOME/.local/bin)
  AUTOSKILLS_DEV_INSTALL_LABEL  installed version label (default: <git-describe>-dev)
  CRUX_INSTALL_ROOT         legacy alias for AUTOSKILLS_INSTALL_ROOT
  CRUX_BIN_DIR              legacy alias for AUTOSKILLS_BIN_DIR
  CRUX_DEV_INSTALL_LABEL    legacy alias for AUTOSKILLS_DEV_INSTALL_LABEL

Examples:
  ./scripts/install_local_dev.sh
  AUTOSKILLS_INSTALL_ROOT="$HOME/.local/share/autoskills-dev" ./scripts/install_local_dev.sh
EOF
}

say() {
  printf 'autoskills-dev-install: %s\n' "$*" >&2
}

die() {
  say "$*"
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

create_wrapper() {
  bin_path="$1"
  target_path="$2"
  cat >"$bin_path" <<EOF
#!/bin/sh
set -eu
exec "$target_path" "\$@"
EOF
  chmod 755 "$bin_path"
}

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"

INSTALL_ROOT="${AUTOSKILLS_INSTALL_ROOT:-${CRUX_INSTALL_ROOT:-$HOME/.local/share/autoskills}}"
BIN_DIR="${AUTOSKILLS_BIN_DIR:-${CRUX_BIN_DIR:-$HOME/.local/bin}}"
LABEL="${AUTOSKILLS_DEV_INSTALL_LABEL:-${CRUX_DEV_INSTALL_LABEL:-}}"

while [ $# -gt 0 ]; do
  case "$1" in
    --install-root)
      [ $# -ge 2 ] || die "--install-root requires a value"
      INSTALL_ROOT="$2"
      shift 2
      ;;
    --bin-dir)
      [ $# -ge 2 ] || die "--bin-dir requires a value"
      BIN_DIR="$2"
      shift 2
      ;;
    --label)
      [ $# -ge 2 ] || die "--label requires a value"
      LABEL="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

need_cmd git
need_cmd go
need_cmd mkdir
need_cmd chmod

VERSION="$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

if [ -z "$LABEL" ]; then
  LABEL="$VERSION-dev"
fi

VERSION_DIR="$INSTALL_ROOT/$LABEL"
CURRENT_LINK="$INSTALL_ROOT/current"
PRIMARY_BIN_PATH="$BIN_DIR/autoskills"
LEGACY_BIN_PATH="$BIN_DIR/crux"
TMP_BIN="$VERSION_DIR/autoskills.tmp"
TARGET_BIN="$VERSION_DIR/autoskills"

LDFLAGS="-X github.com/Royaltyprogram/aiops/pkg/buildinfo.Version=$VERSION -X github.com/Royaltyprogram/aiops/pkg/buildinfo.Commit=$COMMIT -X github.com/Royaltyprogram/aiops/pkg/buildinfo.Date=$BUILD_DATE"

mkdir -p "$INSTALL_ROOT" "$BIN_DIR" "$VERSION_DIR"

say "building current repo CLI from $REPO_ROOT"
go build -ldflags "$LDFLAGS" -o "$TMP_BIN" "$REPO_ROOT/cmd/crux"
mv "$TMP_BIN" "$TARGET_BIN"
chmod 755 "$TARGET_BIN"

ln -sfn "$VERSION_DIR" "$CURRENT_LINK"
create_wrapper "$PRIMARY_BIN_PATH" "$CURRENT_LINK/autoskills"
create_wrapper "$LEGACY_BIN_PATH" "$CURRENT_LINK/autoskills"

CODEX_ROOT="${CODEX_HOME:-$HOME/.codex}"
if [ -d "$CODEX_ROOT" ] || [ -f "$CODEX_ROOT/AGENTS.md" ]; then
  if "$CURRENT_LINK/autoskills" skills ensure-agents --codex-home "$CODEX_ROOT" >/dev/null 2>&1; then
    say "updated Codex AGENTS.md at $CODEX_ROOT/AGENTS.md"
  else
    say "warning: could not update Codex AGENTS.md at $CODEX_ROOT/AGENTS.md"
  fi
fi

say "installed current repo build to $VERSION_DIR"
say "wrappers updated at $PRIMARY_BIN_PATH and $LEGACY_BIN_PATH"
say "saved login/workspace state in ~/.autoskills is preserved across local build updates"
say "next step: autoskills version"
printf 'autoskills %s installed from local repo\n' "$VERSION"
