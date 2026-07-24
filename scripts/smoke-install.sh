#!/bin/sh
# Offline install-fallback smoke test.
#
# It generates fake release assets locally (a .tar.gz plus checksums.txt named
# exactly as the published release and matching the manifest version and this
# platform), then runs scripts/build.sh with Go removed from PATH so the script
# must take the download path: fetch the archive, verify its SHA-256 checksum,
# and install the binary. The archive is served over file:// via the build
# script's release-base-URL override; the checksum is still enforced.
#
# Building the fake asset embeds the real frontend, so this needs Go and Node to
# produce the asset; the path under test (download + checksum + install) needs
# neither once Go is hidden.
set -eu

ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
WEB_DIR="$ROOT_DIR/web"
BIN_NAME="herdr-phone"

log() { printf '%s\n' "smoke-install: $*" >&2; }
fail() { log "error: $*"; exit 1; }

command -v go >/dev/null 2>&1 || fail "go is required to build the fake release asset"
command -v node >/dev/null 2>&1 || fail "node is required to build the embedded frontend for the fake asset"
command -v npm >/dev/null 2>&1 || fail "npm is required to build the embedded frontend for the fake asset"

VERSION="$(sed -n 's/^version[[:space:]]*=[[:space:]]*"\([^"]*\)".*$/\1/p' "$ROOT_DIR/herdr-plugin.toml" | head -n1)"
[ -n "$VERSION" ] || fail "could not read manifest version"

case "$(uname -s)" in
  Linux) GOOS=linux ;;
  Darwin) GOOS=darwin ;;
  *) fail "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) GOARCH=amd64 ;;
  arm64 | aarch64) GOARCH=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

WORK=""
cleanup() { [ -n "$WORK" ] && rm -rf "$WORK"; }
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM
WORK="$(mktemp -d)"
REL="$WORK/release"
mkdir -p "$REL"

# Build the embedded frontend once, then the fake released binary exactly as
# build.sh's download path expects (a bare binary inside the archive).
log "building the embedded frontend for the fake asset"
( cd "$WEB_DIR" && npm ci && npm run build )
[ -d "$WEB_DIR/dist" ] || fail "frontend build did not produce web/dist"
( cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -o "$WORK/$BIN_NAME" ./cmd/herdr-phone )

archive="${BIN_NAME}_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
( cd "$WORK" && tar -czf "$REL/$archive" "$BIN_NAME" )
if command -v sha256sum >/dev/null 2>&1; then
  ( cd "$REL" && sha256sum "$archive" > checksums.txt )
elif command -v shasum >/dev/null 2>&1; then
  ( cd "$REL" && shasum -a 256 "$archive" > checksums.txt )
else
  fail "sha256sum or shasum is required"
fi

# Remove Go's directory from PATH so build.sh cannot build from source and must
# take the download path.
go_dir="$(dirname "$(command -v go)")"
nogo_path="$(printf '%s' "${PATH}" | tr ':' '\n' | grep -v -x "$go_dir" | paste -sd: -)"
[ -n "$nogo_path" ] || fail "could not construct a Go-free PATH"

rm -rf "${ROOT_DIR:?}/bin"
log "running build.sh with Go hidden; downloading from file://$REL"
env PATH="$nogo_path" HERDR_PHONE_RELEASE_BASE_URL="file://$REL" sh "$ROOT_DIR/scripts/build.sh"

[ -x "$ROOT_DIR/bin/$BIN_NAME" ] || fail "build.sh did not install the binary"
got="$("$ROOT_DIR/bin/$BIN_NAME" version | awk '{print $NF}')"
[ "$got" = "$VERSION" ] || fail "installed binary version $got != manifest $VERSION"
log "OK: offline fallback downloaded, checksum-verified, and installed $BIN_NAME $got"
