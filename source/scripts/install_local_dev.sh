#!/bin/sh

set -eu

usage() {
  cat <<'EOF'
Install the current repository's crux CLI into the local wrapper location.

Usage:
  ./scripts/install_local_dev.sh [--install-root <dir>] [--bin-dir <dir>] [--label <name>]

Environment overrides:
  CRUX_INSTALL_ROOT     install root (default: $HOME/.local/share/crux)
  CRUX_BIN_DIR          wrapper script directory (default: $HOME/.local/bin)
  CRUX_DEV_INSTALL_LABEL  installed version label (default: <git-describe>-dev)

Examples:
  ./scripts/install_local_dev.sh
  CRUX_INSTALL_ROOT="$HOME/.local/share/crux-dev" ./scripts/install_local_dev.sh
EOF
}

say() {
  printf 'crux-dev-install: %s\n' "$*" >&2
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

INSTALL_ROOT="${CRUX_INSTALL_ROOT:-$HOME/.local/share/crux}"
BIN_DIR="${CRUX_BIN_DIR:-$HOME/.local/bin}"
LABEL="${CRUX_DEV_INSTALL_LABEL:-}"

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
BIN_PATH="$BIN_DIR/crux"
TMP_BIN="$VERSION_DIR/crux.tmp"
TARGET_BIN="$VERSION_DIR/crux"

LDFLAGS="-X github.com/Royaltyprogram/aiops/pkg/buildinfo.Version=$VERSION -X github.com/Royaltyprogram/aiops/pkg/buildinfo.Commit=$COMMIT -X github.com/Royaltyprogram/aiops/pkg/buildinfo.Date=$BUILD_DATE"

mkdir -p "$INSTALL_ROOT" "$BIN_DIR" "$VERSION_DIR"

say "building current repo CLI from $REPO_ROOT"
go build -ldflags "$LDFLAGS" -o "$TMP_BIN" "$REPO_ROOT/cmd/crux"
mv "$TMP_BIN" "$TARGET_BIN"
chmod 755 "$TARGET_BIN"

ln -sfn "$VERSION_DIR" "$CURRENT_LINK"
create_wrapper "$BIN_PATH" "$CURRENT_LINK/crux"

say "installed current repo build to $VERSION_DIR"
say "wrapper updated at $BIN_PATH"
say "next steps: crux reset && crux version"
printf 'crux %s installed from local repo\n' "$VERSION"
