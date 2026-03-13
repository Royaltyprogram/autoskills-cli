#!/bin/sh

set -eu

usage() {
  cat <<'EOF'
agentopt install script

Usage:
  sh install.sh [--version <tag>] [--install-root <dir>] [--bin-dir <dir>]

Release installs use a prebuilt binary. Go is not required.
When Node.js is missing or too old, the installer can provision a local runtime automatically.

Environment overrides:
  AGENTOPT_VERSION           release tag to install (default: newest published release)
  AGENTOPT_REPO              GitHub repo in owner/name form (default: Royaltyprogram/aiops)
  AGENTOPT_RELEASE_BASE_URL  release download base URL (default: https://github.com/<repo>/releases/download)
  AGENTOPT_RELEASES_API_URL  releases API URL used when AGENTOPT_VERSION is unset
  AGENTOPT_GITHUB_TOKEN      optional token used for GitHub API requests
  AGENTOPT_INSTALL_ROOT      install root (default: $HOME/.local/share/agentopt)
  AGENTOPT_BIN_DIR           wrapper script directory (default: $HOME/.local/bin)
  AGENTOPT_INSTALL_NODE      Node.js install mode: auto, always, never (default: auto)
  AGENTOPT_NODE_VERSION      Node.js version used for local runtime install (default: 20.11.1)
  AGENTOPT_NODE_DIST_BASE_URL  Node.js distribution base URL (default: https://nodejs.org/dist)

Examples:
  curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/aiops/main/scripts/install.sh | sh
  AGENTOPT_VERSION=0.1.0-beta.1 curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/aiops/main/scripts/install.sh | sh
EOF
}

say() {
  printf 'agentopt-install: %s\n' "$*" >&2
}

die() {
  say "$*"
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

json_string_field() {
  key="$1"
  file="$2"
  awk -v key="$key" '
    index($0, "\"" key "\"") {
      line = $0
      sub(/^.*"[^\"]+"[[:space:]]*:[[:space:]]*"/, "", line)
      sub(/".*$/, "", line)
      print line
      exit
    }
  ' "$file"
}

fetch_to_file() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    if [ -n "${AGENTOPT_GITHUB_TOKEN:-}" ]; then
      curl -fsSL -H "Authorization: Bearer ${AGENTOPT_GITHUB_TOKEN}" "$url" -o "$dest"
    else
      curl -fsSL "$url" -o "$dest"
    fi
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    if [ -n "${AGENTOPT_GITHUB_TOKEN:-}" ]; then
      wget -q --header="Authorization: Bearer ${AGENTOPT_GITHUB_TOKEN}" -O "$dest" "$url"
    else
      wget -q -O "$dest" "$url"
    fi
    return
  fi
  die "curl or wget is required"
}

fetch_to_stdout() {
  url="$1"
  if command -v curl >/dev/null 2>&1; then
    if [ -n "${AGENTOPT_GITHUB_TOKEN:-}" ]; then
      curl -fsSL -H "Authorization: Bearer ${AGENTOPT_GITHUB_TOKEN}" "$url"
    else
      curl -fsSL "$url"
    fi
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    if [ -n "${AGENTOPT_GITHUB_TOKEN:-}" ]; then
      wget -q --header="Authorization: Bearer ${AGENTOPT_GITHUB_TOKEN}" -O - "$url"
    else
      wget -q -O - "$url"
    fi
    return
  fi
  die "curl or wget is required"
}

latest_version() {
  api_url="$1"
  tmpfile="$2/releases.json"
  fetch_to_file "$api_url" "$tmpfile"
  version="$(tr ',' '\n' <"$tmpfile" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$version" ] || die "unable to determine latest release tag from $api_url"
  printf '%s\n' "$version"
}

sha256_file() {
  file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$file" | awk '{print $NF}'
    return
  fi
  die "sha256sum, shasum, or openssl is required"
}

extract_archive_dir() {
  archive="$1"
  dest="$2"
  tar -xzf "$archive" -C "$dest"
  first_entry="$(tar -tzf "$archive" | head -n 1)"
  [ -n "$first_entry" ] || die "archive is empty: $archive"
  first_dir="${first_entry%%/*}"
  [ -n "$first_dir" ] || die "failed to determine extracted bundle directory"
  printf '%s\n' "$dest/$first_dir"
}

create_wrapper() {
  bin_path="$1"
  target_path="$2"
  node_bin_path="$3"
  cat >"$bin_path" <<EOF
#!/bin/sh
set -eu
if [ -d "$node_bin_path" ]; then
  PATH="$node_bin_path:\${PATH:-}"
  export PATH
fi
exec "$target_path" "\$@"
EOF
  chmod 755 "$bin_path"
}

normalize_version_tag() {
  version="$1"
  case "$version" in
    v*) printf '%s\n' "$version" ;;
    *) printf 'v%s\n' "$version" ;;
  esac
}

node_major_version() {
  version="${1#v}"
  printf '%s\n' "$version" | awk -F. '{print $1}'
}

has_compatible_node() {
  if ! command -v node >/dev/null 2>&1; then
    return 1
  fi
  current_version="$(node --version 2>/dev/null || true)"
  [ -n "$current_version" ] || return 1
  current_major="$(node_major_version "$current_version")"
  [ -n "$current_major" ] || return 1
  [ "$current_major" -ge 18 ]
}

install_node_runtime() {
  install_root="$1"
  node_platform="$2"
  node_arch="$3"
  install_mode="$4"
  node_version="$5"
  node_dist_base_url="$6"

  case "$install_mode" in
    auto|always|never) ;;
    *) die "invalid AGENTOPT_INSTALL_NODE value: $install_mode" ;;
  esac

  if [ "$install_mode" = "never" ]; then
    say "skipping Node.js install because AGENTOPT_INSTALL_NODE=never"
    return
  fi

  if [ "$install_mode" = "auto" ] && has_compatible_node; then
    say "using existing Node.js $(node --version)"
    return
  fi

  normalized_version="$(normalize_version_tag "$node_version")"
  archive_base="node-$normalized_version-$node_platform-$node_arch"
  archive_name="$archive_base.tar.gz"
  archive_url="$node_dist_base_url/$normalized_version/$archive_name"
  checksum_url="$node_dist_base_url/$normalized_version/SHASUMS256.txt"
  archive_path="$TMPDIR_WORK/$archive_name"
  checksum_path="$TMPDIR_WORK/SHASUMS256.txt"
  extract_dir="$TMPDIR_WORK/node-extract"
  node_root="$install_root/node"
  version_dir="$node_root/$normalized_version"
  current_link="$node_root/current"
  stage_dir="$node_root/.install-$normalized_version.$$"

  say "installing Node.js $normalized_version from $archive_url"
  fetch_to_file "$archive_url" "$archive_path"
  fetch_to_file "$checksum_url" "$checksum_path"

  expected_sha="$(awk -v name="$archive_name" '$2 == name {print $1; exit}' "$checksum_path")"
  [ -n "$expected_sha" ] || die "unable to find checksum for $archive_name"
  actual_sha="$(sha256_file "$archive_path")"
  [ "$expected_sha" = "$actual_sha" ] || die "checksum mismatch for $archive_name"

  mkdir -p "$node_root" "$extract_dir"
  extracted_dir="$(extract_archive_dir "$archive_path" "$extract_dir")"
  [ -x "$extracted_dir/bin/node" ] || die "node archive is missing bin/node"

  rm -rf "$stage_dir"
  mv "$extracted_dir" "$stage_dir"
  rm -rf "$version_dir"
  mv "$stage_dir" "$version_dir"
  ln -sfn "$version_dir" "$current_link"
  say "installed local Node.js runtime to $version_dir"
}

VERSION="${AGENTOPT_VERSION:-}"
INSTALL_ROOT="${AGENTOPT_INSTALL_ROOT:-$HOME/.local/share/agentopt}"
BIN_DIR="${AGENTOPT_BIN_DIR:-$HOME/.local/bin}"
REPO="${AGENTOPT_REPO:-Royaltyprogram/aiops}"
RELEASE_BASE_URL="${AGENTOPT_RELEASE_BASE_URL:-https://github.com/$REPO/releases/download}"
RELEASES_API_URL="${AGENTOPT_RELEASES_API_URL:-https://api.github.com/repos/$REPO/releases?per_page=20}"
INSTALL_NODE="${AGENTOPT_INSTALL_NODE:-auto}"
NODE_VERSION="${AGENTOPT_NODE_VERSION:-20.11.1}"
NODE_DIST_BASE_URL="${AGENTOPT_NODE_DIST_BASE_URL:-https://nodejs.org/dist}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      [ $# -ge 2 ] || die "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
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
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

need_cmd uname
need_cmd tar
need_cmd mktemp
need_cmd chmod
need_cmd mkdir

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin) GOOS="darwin" ;;
  linux) GOOS="linux" ;;
  *) die "unsupported operating system: $OS" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  arm64|aarch64) GOARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac
case "$GOARCH" in
  amd64) NODE_ARCH="x64" ;;
  arm64) NODE_ARCH="arm64" ;;
  *) die "unsupported node architecture: $GOARCH" ;;
esac
NODE_PLATFORM="$GOOS"

TMPDIR_ROOT="${TMPDIR:-/tmp}"
TMPDIR_WORK="$(mktemp -d "$TMPDIR_ROOT/agentopt-install.XXXXXX")"
cleanup() {
  rm -rf "$TMPDIR_WORK"
}
trap cleanup EXIT INT TERM

if [ -z "$VERSION" ]; then
  say "resolving latest release tag from $RELEASES_API_URL"
  VERSION="$(latest_version "$RELEASES_API_URL" "$TMPDIR_WORK")"
fi

BUNDLE_NAME="agentopt-$VERSION-$GOOS-$GOARCH"
ARCHIVE_NAME="$BUNDLE_NAME.tar.gz"
CHECKSUM_NAME="$ARCHIVE_NAME.sha256"
ARCHIVE_URL="$RELEASE_BASE_URL/$VERSION/$ARCHIVE_NAME"
CHECKSUM_URL="$RELEASE_BASE_URL/$VERSION/$CHECKSUM_NAME"
ARCHIVE_PATH="$TMPDIR_WORK/$ARCHIVE_NAME"
CHECKSUM_PATH="$TMPDIR_WORK/$CHECKSUM_NAME"

say "downloading $ARCHIVE_URL"
fetch_to_file "$ARCHIVE_URL" "$ARCHIVE_PATH"
fetch_to_file "$CHECKSUM_URL" "$CHECKSUM_PATH"

EXPECTED_SHA="$(awk '{print $1}' "$CHECKSUM_PATH")"
ACTUAL_SHA="$(sha256_file "$ARCHIVE_PATH")"
[ "$EXPECTED_SHA" = "$ACTUAL_SHA" ] || die "checksum mismatch for $ARCHIVE_NAME"

EXTRACT_DIR="$TMPDIR_WORK/extract"
mkdir -p "$EXTRACT_DIR"
BUNDLE_DIR="$(extract_archive_dir "$ARCHIVE_PATH" "$EXTRACT_DIR")"
[ -x "$BUNDLE_DIR/agentopt" ] || die "bundle is missing agentopt executable"

VERSION_DIR="$INSTALL_ROOT/$VERSION"
CURRENT_LINK="$INSTALL_ROOT/current"
BIN_PATH="$BIN_DIR/agentopt"
STAGE_DIR="$INSTALL_ROOT/.install-$VERSION.$$"
LOCAL_NODE_BIN="$INSTALL_ROOT/node/current/bin"

mkdir -p "$INSTALL_ROOT" "$BIN_DIR"
install_node_runtime "$INSTALL_ROOT" "$NODE_PLATFORM" "$NODE_ARCH" "$INSTALL_NODE" "$NODE_VERSION" "$NODE_DIST_BASE_URL"
rm -rf "$STAGE_DIR"
cp -R "$BUNDLE_DIR" "$STAGE_DIR"
rm -rf "$VERSION_DIR"
mv "$STAGE_DIR" "$VERSION_DIR"
ln -sfn "$VERSION_DIR" "$CURRENT_LINK"
create_wrapper "$BIN_PATH" "$CURRENT_LINK/agentopt" "$LOCAL_NODE_BIN"

say "installed $VERSION to $VERSION_DIR"
say "wrapper created at $BIN_PATH"
say "release install uses a prebuilt agentopt binary; Go is not required"
say "next step: agentopt setup --server <agentopt-server-url>"
if [ -x "$LOCAL_NODE_BIN/node" ]; then
  say "agentopt will use local Node.js from $LOCAL_NODE_BIN when needed"
elif command -v node >/dev/null 2>&1; then
  say "agentopt will use system Node.js $(node --version)"
else
  say "node runtime not found; install it manually only if you need other Node-based tooling"
fi
if ! command -v agentopt >/dev/null 2>&1; then
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) say "add $BIN_DIR to PATH to run agentopt directly" ;;
  esac
fi

printf 'agentopt %s installed\n' "$VERSION"
