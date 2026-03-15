#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_DIR="${RELEASE_DIR:-$ROOT_DIR/output/release}"
VERSION_LABEL="${VERSION_LABEL:-}"
REPO="${GITHUB_REPOSITORY:-}"
TITLE="${RELEASE_TITLE:-}"
NOTES_FILE="${RELEASE_NOTES_FILE:-}"
DRAFT="${DRAFT:-0}"
PRERELEASE="${PRERELEASE:-0}"
LATEST_MODE="${LATEST_MODE:-}"
TARGET="${TARGET_COMMITISH:-}"
GH_BIN="${CRUX_GH_BIN:-gh}"

usage() {
  cat <<'EOF'
Publish Crux release assets to GitHub Releases.

Usage:
  ./scripts/publish_github_release.sh [options]

Options:
  --version <tag>       release tag to publish
  --repo <owner/name>   GitHub repository override
  --title <text>        release title override
  --notes-file <path>   release notes markdown file
  --target <ref>        target branch or commit for the release tag
  --draft               create or update as draft
  --prerelease          create or update as prerelease
  --latest <true|false> set latest flag when creating a new release
  --help                show this help

Environment:
  VERSION_LABEL         default version tag
  RELEASE_DIR           directory containing built release assets
  TARGET_COMMITISH      target branch or commit for the release tag
  GITHUB_REPOSITORY     default repo in owner/name form
  GITHUB_TOKEN or GH_TOKEN must be available for gh auth
EOF
}

say() {
  printf 'publish-release: %s\n' "$*" >&2
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

detect_version() {
  if [[ -n "$VERSION_LABEL" ]]; then
    printf '%s\n' "$VERSION_LABEL"
    return
  fi

  local newest_index
  newest_index="$(find "$RELEASE_DIR" -maxdepth 1 -name 'crux-*.release-index.json' -type f | sort | tail -n 1)"
  if [[ -n "$newest_index" ]]; then
    basename "$newest_index" .release-index.json | sed 's/^crux-//'
    return
  fi

  git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo beta
}

COLLECTED_ASSETS=()

collect_assets() {
  local version="$1"

  shopt -s nullglob
  local assets=(
    "$RELEASE_DIR"/crux-"$version"-*.tar.gz
    "$RELEASE_DIR"/crux-"$version"-*.tar.gz.sha256
    "$RELEASE_DIR"/crux-"$version"-*.json
    "$RELEASE_DIR"/crux-"$version".release-index.json
    "$RELEASE_DIR"/crux-"$version".release-index.json.sha256
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

  [[ "${#deduped[@]}" -gt 0 ]] || die "no release assets found for version $version in $RELEASE_DIR"
  COLLECTED_ASSETS=("${deduped[@]}")
}

default_title() {
  local version="$1"
  printf 'Crux %s\n' "$version"
}

default_prerelease_for_version() {
  local version="$1"
  if [[ "$version" == *alpha* || "$version" == *beta* || "$version" == *rc* ]]; then
    printf '1\n'
    return
  fi
  printf '0\n'
}

generate_default_notes() {
  local version="$1"
  shift
  local assets=("$@")
  local raw_url="https://raw.githubusercontent.com/$REPO/$version/scripts/install.sh"

  cat <<EOF
# Crux $version

One-command install:

\`\`\`bash
curl -fsSL $raw_url | sh
CRUX_VERSION=$version curl -fsSL $raw_url | sh
\`\`\`

Included assets:
$(for path in "${assets[@]}"; do printf -- '- `%s`\n' "$(basename "$path")"; done)
EOF
}

release_exists() {
  local repo="$1"
  local version="$2"
  "$GH_BIN" release view "$version" --repo "$repo" >/dev/null 2>&1
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
    --title)
      [[ $# -ge 2 ]] || die "--title requires a value"
      TITLE="$2"
      shift 2
      ;;
    --target)
      [[ $# -ge 2 ]] || die "--target requires a value"
      TARGET="$2"
      shift 2
      ;;
    --notes-file)
      [[ $# -ge 2 ]] || die "--notes-file requires a value"
      NOTES_FILE="$2"
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
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

need_cmd "$GH_BIN"
need_cmd git
need_cmd find

REPO="$(detect_repo)"
VERSION_LABEL="$(detect_version)"
TITLE="${TITLE:-$(default_title "$VERSION_LABEL")}"
if [[ "$PRERELEASE" -eq 0 ]]; then
  PRERELEASE="$(default_prerelease_for_version "$VERSION_LABEL")"
fi

declare -a ASSETS=()
collect_assets "$VERSION_LABEL"
ASSETS=("${COLLECTED_ASSETS[@]}")

TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/crux-release-notes.XXXXXX")"
cleanup() {
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

if [[ -z "$NOTES_FILE" ]]; then
  NOTES_FILE="$TMPDIR/release-notes.md"
  generate_default_notes "$VERSION_LABEL" "${ASSETS[@]}" >"$NOTES_FILE"
fi

say "repo=$REPO version=$VERSION_LABEL assets=${#ASSETS[@]}"

if release_exists "$REPO" "$VERSION_LABEL"; then
  say "release $VERSION_LABEL exists; uploading assets with --clobber"
  "$GH_BIN" release upload "$VERSION_LABEL" "${ASSETS[@]}" --repo "$REPO" --clobber
  edit_args=(
    release edit "$VERSION_LABEL"
    --repo "$REPO"
    --title "$TITLE"
    --notes-file "$NOTES_FILE"
  )
  if [[ -n "$TARGET" ]]; then
    edit_args+=(--target "$TARGET")
  fi
  if [[ "$DRAFT" -eq 1 ]]; then
    edit_args+=(--draft)
  fi
  if [[ "$PRERELEASE" -eq 1 ]]; then
    edit_args+=(--prerelease)
  fi
  "$GH_BIN" "${edit_args[@]}"
else
  create_args=(
    release create "$VERSION_LABEL"
    --repo "$REPO"
    --title "$TITLE"
    --notes-file "$NOTES_FILE"
  )
  if [[ -n "$TARGET" ]]; then
    create_args+=(--target "$TARGET")
  fi
  if [[ "$DRAFT" -eq 1 ]]; then
    create_args+=(--draft)
  fi
  if [[ "$PRERELEASE" -eq 1 ]]; then
    create_args+=(--prerelease)
  fi
  if [[ "$LATEST_MODE" == "true" ]]; then
    create_args+=(--latest)
  elif [[ "$LATEST_MODE" == "false" ]]; then
    create_args+=(--latest=false)
  fi
  create_args+=("${ASSETS[@]}")

  say "creating release $VERSION_LABEL"
  "$GH_BIN" "${create_args[@]}"
fi

say "published https://github.com/$REPO/releases/tag/$VERSION_LABEL"
