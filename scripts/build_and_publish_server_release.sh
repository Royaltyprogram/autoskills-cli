#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"

load_env_file() {
  if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
  fi
}

load_env_file

RELEASE_DIR="${RELEASE_DIR:-$ROOT_DIR/output/release}"
VERSION_LABEL="${VERSION_LABEL:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo beta)}"
REPO="${GITHUB_REPOSITORY:-}"
TITLE="${RELEASE_TITLE:-}"
NOTES_FILE="${RELEASE_NOTES_FILE:-}"
TARGETS="${SERVER_RELEASE_TARGETS:-linux/amd64}"
TARGET_COMMITISH="${TARGET_COMMITISH:-}"
DRAFT="${DRAFT:-0}"
PRERELEASE="${PRERELEASE:-0}"
LATEST_MODE="${LATEST_MODE:-}"
DRY_RUN="${DRY_RUN:-0}"
GH_BIN="${CRUX_GH_BIN:-gh}"

usage() {
  cat <<'EOF'
Build server release bundles and upload them to a GitHub Release.

Usage:
  ./scripts/build_and_publish_server_release.sh [options]

Options:
  --version <tag>       release tag to build and publish
  --repo <owner/name>   GitHub repository override
  --targets <list>      comma-separated GOOS/GOARCH pairs (default: linux/amd64)
  --title <text>        release title when creating a new release
  --notes-file <path>   release notes markdown file when creating a new release
  --target <ref>        target branch or commit when creating a new tag-backed release
  --draft               create release as draft when it does not exist yet
  --prerelease          create release as prerelease when it does not exist yet
  --latest <true|false> set latest flag when creating a new release
  --dry-run             build assets and print the publish plan without calling GitHub
  --help                show this help

Environment:
  ENV_FILE                 env file to load before resolving defaults (default: <repo>/.env)
  VERSION_LABEL            default release tag
  SERVER_RELEASE_TARGETS   comma-separated GOOS/GOARCH pairs
  RELEASE_DIR              output directory for built assets
  TARGET_COMMITISH         target branch or commit for release creation
  GITHUB_REPOSITORY        default repo in owner/name form
  GITHUB_TOKEN or GH_TOKEN must be available for gh auth when not using --dry-run
EOF
}

say() {
  printf 'server-release: %s\n' "$*" >&2
}

die() {
  say "$*"
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

detect_repo() {
  if [[ -n "$REPO" ]]; then
    printf '%s\n' "$REPO"
    return
  fi

  local remote
  remote="$(git -C "$ROOT_DIR" config --get remote.origin.url 2>/dev/null || true)"
  if [[ -z "$remote" ]]; then
    die "unable to infer GitHub repository; pass --repo owner/name"
  fi

  remote="${remote%.git}"
  remote="${remote#git@github.com:}"
  remote="${remote#https://github.com/}"
  remote="${remote#http://github.com/}"
  [[ "$remote" == */* ]] || die "unable to parse GitHub repository from origin remote: $remote"
  printf '%s\n' "$remote"
}

release_exists() {
  local repo="$1"
  local version="$2"
  "$GH_BIN" release view "$version" --repo "$repo" >/dev/null 2>&1
}

default_title() {
  local version="$1"
  printf 'Crux %s\n' "$version"
}

default_notes() {
  local version="$1"
  shift
  local assets=("$@")

  cat <<EOF
# Crux $version

Server release bundles:
$(for path in "${assets[@]}"; do printf -- '- `%s`\n' "$(basename "$path")"; done)

Extract the matching archive for your server host and run:

\`\`\`bash
APP_MODE=prod ./server
\`\`\`
EOF
}

collect_assets() {
  shopt -s nullglob
  local assets=(
    "$RELEASE_DIR"/crux-server-"$VERSION_LABEL"-*.tar.gz
    "$RELEASE_DIR"/crux-server-"$VERSION_LABEL"-*.tar.gz.sha256
    "$RELEASE_DIR"/crux-server-"$VERSION_LABEL"-*.json
  )
  shopt -u nullglob

  local deduped=()
  local seen=""
  local path base
  for path in "${assets[@]}"; do
    [[ -f "$path" ]] || continue
    base="$(basename "$path")"
    if [[ " $seen " == *" $base "* ]]; then
      continue
    fi
    seen+=" $base"
    deduped+=("$path")
  done

  [[ "${#deduped[@]}" -gt 0 ]] || die "no server release assets found for version $VERSION_LABEL in $RELEASE_DIR"
  printf '%s\n' "${deduped[@]}"
}

build_targets() {
  local raw_targets="$1"
  local item goos goarch

  IFS=',' read -r -a target_items <<<"$raw_targets"
  [[ "${#target_items[@]}" -gt 0 ]] || die "no server release targets provided"
  for item in "${target_items[@]}"; do
    item="${item//[[:space:]]/}"
    [[ -n "$item" ]] || continue
    if [[ "$item" != */* ]]; then
      die "invalid target '$item'; expected GOOS/GOARCH"
    fi
    goos="${item%%/*}"
    goarch="${item##*/}"
    [[ -n "$goos" && -n "$goarch" ]] || die "invalid target '$item'; expected GOOS/GOARCH"
    say "building server bundle for $goos/$goarch"
    GOOS="$goos" GOARCH="$goarch" VERSION_LABEL="$VERSION_LABEL" RELEASE_DIR="$RELEASE_DIR" \
      "$ROOT_DIR/scripts/build_server_bundle.sh" >/dev/null
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || die "--version requires a value"
      VERSION_LABEL="$2"
      shift 2
      ;;
    --repo)
      [[ $# -ge 2 ]] || die "--repo requires a value"
      REPO="$2"
      shift 2
      ;;
    --targets)
      [[ $# -ge 2 ]] || die "--targets requires a value"
      TARGETS="$2"
      shift 2
      ;;
    --title)
      [[ $# -ge 2 ]] || die "--title requires a value"
      TITLE="$2"
      shift 2
      ;;
    --notes-file)
      [[ $# -ge 2 ]] || die "--notes-file requires a value"
      NOTES_FILE="$2"
      shift 2
      ;;
    --target)
      [[ $# -ge 2 ]] || die "--target requires a value"
      TARGET_COMMITISH="$2"
      shift 2
      ;;
    --draft)
      DRAFT=1
      shift
      ;;
    --prerelease)
      PRERELEASE=1
      shift
      ;;
    --latest)
      [[ $# -ge 2 ]] || die "--latest requires true or false"
      LATEST_MODE="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
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
need_cmd find
if [[ "$DRY_RUN" != "1" ]]; then
  need_cmd "$GH_BIN"
fi

REPO="$(detect_repo)"
TITLE="${TITLE:-$(default_title "$VERSION_LABEL")}"

build_targets "$TARGETS"

ASSETS=()
while IFS= read -r asset; do
  ASSETS+=("$asset")
done < <(collect_assets)
say "built ${#ASSETS[@]} server assets for $VERSION_LABEL"

TMPDIR_WORK="$(mktemp -d "${TMPDIR:-/tmp}/crux-server-release.XXXXXX")"
cleanup() {
  rm -rf "$TMPDIR_WORK"
}
trap cleanup EXIT

if [[ -z "$NOTES_FILE" ]]; then
  NOTES_FILE="$TMPDIR_WORK/release-notes.md"
  default_notes "$VERSION_LABEL" "${ASSETS[@]}" >"$NOTES_FILE"
fi

if [[ "$DRY_RUN" == "1" ]]; then
  say "dry-run repo=$REPO version=$VERSION_LABEL targets=$TARGETS"
  printf '%s\n' "${ASSETS[@]}"
  exit 0
fi

if release_exists "$REPO" "$VERSION_LABEL"; then
  say "release $VERSION_LABEL exists; uploading server assets with --clobber"
  "$GH_BIN" release upload "$VERSION_LABEL" "${ASSETS[@]}" --repo "$REPO" --clobber
else
  create_args=(
    release create "$VERSION_LABEL"
    --repo "$REPO"
    --title "$TITLE"
    --notes-file "$NOTES_FILE"
  )
  if [[ -n "$TARGET_COMMITISH" ]]; then
    create_args+=(--target "$TARGET_COMMITISH")
  fi
  if [[ "$DRAFT" -eq 1 ]]; then
    create_args+=(--draft)
  fi
  if [[ "$PRERELEASE" -eq 1 ]]; then
    create_args+=(--prerelease)
  fi
  if [[ -n "$LATEST_MODE" ]]; then
    create_args+=(--latest="$LATEST_MODE")
  fi
  create_args+=("${ASSETS[@]}")
  say "creating release $VERSION_LABEL with server assets"
  "$GH_BIN" "${create_args[@]}"
fi

say "published server assets to https://github.com/$REPO/releases/tag/$VERSION_LABEL"
