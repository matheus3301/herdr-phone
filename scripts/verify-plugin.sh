#!/bin/sh
# Validate herdr-plugin.toml against a real Herdr v0.7.5 in fully isolated state.
# It links the plugin, lists it, and confirms the plugin and its global actions
# are discovered, without touching the user's active Herdr session.
#
# herdr-phone v0.2.0 advertises macOS only, so this is meant to run on macOS
# (CI schedules it on a macOS runner). It uses an installed herdr (HERDR_BIN or,
# with HERDR_USE_PATH=1, PATH) whose version is verified to be >= 0.7.5 with a
# working `plugin` command, or downloads the official v0.7.5 binary and verifies
# its pinned SHA-256 before use.
set -eu

HERDR_VERSION="0.7.5"
PLUGIN_ID="matheus3301.phone"
RELEASE_BASE="https://github.com/ogulcancelik/herdr/releases/download/v${HERDR_VERSION}"
ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"

log() { printf '%s\n' "verify-plugin: $*" >&2; }
fail() { log "error: $*"; exit 1; }

WORK=""
cleanup() { [ -n "$WORK" ] && rm -rf "$WORK"; }
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM
WORK="$(mktemp -d)"

# Isolate all state Herdr might read or write.
HOME="$WORK/home"
XDG_CONFIG_HOME="$WORK/config"
XDG_DATA_HOME="$WORK/data"
XDG_STATE_HOME="$WORK/state"
XDG_CACHE_HOME="$WORK/cache"
export HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_CACHE_HOME
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME"

# Never inherit a live session's socket, context, or any tunnel/pairing secret.
unset HERDR_SOCKET_PATH HERDR_ENV HERDR_PLUGIN_ID HERDR_PLUGIN_ROOT \
      HERDR_PLUGIN_CONFIG_DIR HERDR_PLUGIN_STATE_DIR HERDR_PLUGIN_CONTEXT_JSON \
      HERDR_WORKSPACE_ID HERDR_TAB_ID HERDR_PANE_ID \
      TUNNEL_TOKEN TUNNEL_TOKEN_FILE 2>/dev/null || true

fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$1" -O "$2"
  else
    return 1
  fi
}

sha256_of() { # file -> hex
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    return 1
  fi
}

# Pinned v0.7.5 raw-binary assets (the release ships executables, not archives).
asset_for() { # os arch -> "name sha256"
  case "$1/$2" in
    Linux/x86_64|Linux/amd64)   echo "herdr-linux-x86_64 3dc83288073e4c2d3c679a30e7be97bcca9141c6fd17dbbb9219142e95c59253" ;;
    Linux/aarch64|Linux/arm64)  echo "herdr-linux-aarch64 32e763a1499a6b694b1d708e4f062b743be1da9f34fcfa4d212d6db6fe09a8b9" ;;
    Darwin/x86_64|Darwin/amd64) echo "herdr-macos-x86_64 3fe50c4a63dc8102306b1322178628ddb3655cd3ae56d784f094153408d69e62" ;;
    Darwin/arm64|Darwin/aarch64)echo "herdr-macos-aarch64 37350546b0012555943b92eaf962665de4e264395baeb44227b8015e8ff5b0d6" ;;
    *) return 1 ;;
  esac
}

download_herdr() {
  set -- "$(asset_for "$(uname -s)" "$(uname -m)")" || return 1
  entry="$1"
  name="${entry%% *}"
  want="${entry##* }"
  [ -n "$name" ] && [ -n "$want" ] || return 1
  dest="$WORK/herdr"
  fetch "$RELEASE_BASE/$name" "$dest" || return 1
  got="$(sha256_of "$dest")" || return 1
  if [ "$got" != "$want" ]; then
    fail "checksum mismatch for $name (expected $want, got $got)"
  fi
  chmod +x "$dest"
  printf '%s\n' "$dest"
}

# version_ge A B -> true if version A >= B (dotted numeric).
version_ge() {
  a="$1"; b="$2"
  # Intentional word-splitting on IFS=. to split the dotted version components.
  # shellcheck disable=SC2086
  { IFS=.; set -- $a; a1="${1:-0}"; a2="${2:-0}"; a3="${3:-0}"; }
  # shellcheck disable=SC2086
  set -- $b; b1="${1:-0}"; b2="${2:-0}"; b3="${3:-0}"
  unset IFS
  [ "$a1" -gt "$b1" ] && return 0
  [ "$a1" -lt "$b1" ] && return 1
  [ "$a2" -gt "$b2" ] && return 0
  [ "$a2" -lt "$b2" ] && return 1
  [ "$a3" -ge "$b3" ]
}

herdr_version() { # binary -> X.Y.Z or empty
  "$1" --version 2>/dev/null | sed -n 's/.*[^0-9]\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -n1
}

# Resolve a Herdr binary. The pinned official v0.7.5 is used BY DEFAULT so
# verification is reproducible and never silently relies on an arbitrary `herdr`
# from PATH. An explicit HERDR_BIN always wins; set HERDR_USE_PATH=1 to opt into
# a PATH-resolved herdr.
HERDR=""
if [ -n "${HERDR_BIN:-}" ]; then
  HERDR="$HERDR_BIN"
  log "using HERDR_BIN=$HERDR"
elif [ "${HERDR_USE_PATH:-}" = "1" ] && command -v herdr >/dev/null 2>&1; then
  HERDR="$(command -v herdr)"
  log "using PATH herdr=$HERDR (HERDR_USE_PATH=1)"
else
  log "downloading official Herdr v$HERDR_VERSION (pinned checksum)"
  HERDR="$(download_herdr)" || fail "could not download/verify Herdr $HERDR_VERSION; set HERDR_BIN or HERDR_USE_PATH=1"
fi

# Reject a version mismatch on an explicit/PATH binary.
ver="$(herdr_version "$HERDR")"
[ -n "$ver" ] || fail "could not determine the version of $HERDR"
if ! version_ge "$ver" "$HERDR_VERSION"; then
  fail "herdr $ver is older than the required $HERDR_VERSION"
fi

# Some Homebrew bottles report the right version but omit the plugin command.
"$HERDR" plugin --help >/dev/null 2>&1 || \
  fail "this herdr build lacks the 'plugin' command; install the official Herdr $HERDR_VERSION"

log "using herdr $ver ($HERDR)"

"$HERDR" plugin link "$ROOT_DIR" >/dev/null 2>&1 || fail "plugin link failed"
log "linked plugin from $ROOT_DIR"

listing="$("$HERDR" plugin list --json 2>/dev/null)" || fail "plugin list failed"
# The manifest declares global actions and no long-running plugin pane.
for token in "\"plugin_id\":\"$PLUGIN_ID\"" "\"id\":\"start\"" "\"id\":\"start-quick\"" "\"id\":\"stop\"" "\"id\":\"status\"" "\"id\":\"setup-link\"" "\"id\":\"doctor\""; do
  case "$listing" in
    *"$token"*) ;;
    *) fail "plugin listing did not include expected entry: $token" ;;
  esac
done

log "verified: plugin '$PLUGIN_ID' and its global actions are discoverable"
log "OK"
