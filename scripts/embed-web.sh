#!/bin/sh
# Sync the built frontend into the Go embed directory.
#
# The Go binary embeds internal/webui/generated. That directory holds only a
# committed marker (.gitkeep) in a fresh checkout so it always exists in git and
# the package compiles; the real built assets are produced by the frontend build
# and copied in here, and are git-ignored. This script performs that copy
# atomically (build into a staging sibling, then rename into place) and always
# preserves the committed marker.
#
# Usage:
#   scripts/embed-web.sh [sync]   copy WEB_DIST into WEBUI_GENERATED (default)
#   scripts/embed-web.sh --clean  remove generated assets, keep the marker
#   scripts/embed-web.sh --smoke  self-test the sync/clean in a temp dir
#
# Overridable (used by --smoke and by tests): WEB_DIST, WEBUI_GENERATED,
# EMBED_MARKER.
set -eu

ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WEB_DIST="${WEB_DIST:-$ROOT_DIR/web/dist}"
WEBUI_GENERATED="${WEBUI_GENERATED:-$ROOT_DIR/internal/webui/generated}"
EMBED_MARKER="${EMBED_MARKER:-.gitkeep}"

log() { printf '%s\n' "embed-web: $*" >&2; }
fail() { log "error: $*"; exit 1; }

# Ensure the generated directory and its committed marker exist.
ensure_marker() {
  mkdir -p "$WEBUI_GENERATED"
  [ -f "$WEBUI_GENERATED/$EMBED_MARKER" ] || : > "$WEBUI_GENERATED/$EMBED_MARKER"
}

# Remove every top-level entry under the generated directory except the marker.
clean() {
  ensure_marker
  for entry in "$WEBUI_GENERATED"/* "$WEBUI_GENERATED"/.[!.]* "$WEBUI_GENERATED"/..?*; do
    [ -e "$entry" ] || continue
    [ "$(basename "$entry")" = "$EMBED_MARKER" ] && continue
    rm -rf "$entry"
  done
  log "cleaned $WEBUI_GENERATED (kept $EMBED_MARKER)"
}

# Copy WEB_DIST into WEBUI_GENERATED atomically, preserving the marker.
sync() {
  [ -d "$WEB_DIST" ] || fail "frontend build output not found: $WEB_DIST (build the frontend first, e.g. 'make build-web')"
  [ -f "$WEB_DIST/index.html" ] || fail "$WEB_DIST/index.html missing; the frontend build is incomplete"

  ensure_marker

  staging="$WEBUI_GENERATED.staging.$$"
  backup="$WEBUI_GENERATED.old.$$"
  rm -rf "$staging" "$backup"
  mkdir -p "$staging"

  # Populate staging with the built assets, then carry the committed marker in.
  cp -R "$WEB_DIST"/. "$staging"/
  if [ -f "$WEBUI_GENERATED/$EMBED_MARKER" ]; then
    cp -p "$WEBUI_GENERATED/$EMBED_MARKER" "$staging/$EMBED_MARKER"
  else
    : > "$staging/$EMBED_MARKER"
  fi

  # Two renames on the same filesystem; roll back if the swap-in fails.
  if [ -e "$WEBUI_GENERATED" ]; then
    mv "$WEBUI_GENERATED" "$backup"
  fi
  if ! mv "$staging" "$WEBUI_GENERATED"; then
    [ -e "$backup" ] && mv "$backup" "$WEBUI_GENERATED"
    rm -rf "$staging"
    fail "failed to install generated assets into $WEBUI_GENERATED"
  fi
  rm -rf "$backup"
  log "synced $WEB_DIST -> $WEBUI_GENERATED (marker '$EMBED_MARKER' preserved)"
}

# Self-test in an isolated temp tree; never touches the real directories.
smoke() {
  tmp="$(mktemp -d)" || fail "mktemp failed"
  # shellcheck disable=SC2064
  trap "rm -rf \"$tmp\"" EXIT INT TERM
  WEB_DIST="$tmp/dist"
  WEBUI_GENERATED="$tmp/generated"
  mkdir -p "$WEB_DIST/assets"
  printf '<!doctype html>\n' > "$WEB_DIST/index.html"
  printf 'console.log(1)\n' > "$WEB_DIST/assets/app.js"
  mkdir -p "$WEBUI_GENERATED"
  : > "$WEBUI_GENERATED/$EMBED_MARKER"

  sync
  [ -f "$WEBUI_GENERATED/index.html" ] || fail "smoke: index.html not synced"
  [ -f "$WEBUI_GENERATED/assets/app.js" ] || fail "smoke: nested asset not synced"
  [ -f "$WEBUI_GENERATED/$EMBED_MARKER" ] || fail "smoke: marker missing after sync"

  clean
  [ -f "$WEBUI_GENERATED/$EMBED_MARKER" ] || fail "smoke: marker missing after clean"
  [ ! -e "$WEBUI_GENERATED/index.html" ] || fail "smoke: assets remained after clean"
  [ ! -e "$WEBUI_GENERATED/assets" ] || fail "smoke: nested assets remained after clean"
  log "smoke OK"
}

case "${1:-sync}" in
  sync) sync ;;
  --clean | clean) clean ;;
  --smoke | smoke) smoke ;;
  -h | --help) grep '^#' "$0" | grep -v '^#!' | sed 's/^# \{0,1\}//' ;;
  *) fail "unknown argument: $1 (use sync, --clean, or --smoke)" ;;
esac
