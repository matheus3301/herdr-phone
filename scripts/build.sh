#!/bin/sh
# Install-time build for herdr-phone.
#
# Builds from source when compatible Go AND Node toolchains are present: it
# installs the frontend from the committed lockfile, builds web/dist, and embeds
# it into a single static Go binary. Otherwise it downloads and SHA-256-verifies
# the release archive for this exact manifest version and platform.
#
# It never runs `curl | sh`, never evaluates config, and never installs
# cloudflared (see `herdr-phone doctor` for cloudflared guidance). It fails
# closed on any unsupported or unverifiable input.
set -eu

REPO="matheus3301/herdr-phone"
BIN_NAME="herdr-phone"
ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
MANIFEST="$ROOT_DIR/herdr-plugin.toml"
WEB_DIR="$ROOT_DIR/web"
OUT_DIR="$ROOT_DIR/bin"
OUT_BIN="$OUT_DIR/$BIN_NAME"
# Minimum Go for building from source. 1.26.5 is the floor: earlier 1.26 patch
# releases contain reachable standard-library vulnerabilities, so an older 1.26.x
# is treated as incompatible and the checksum-verified release fallback is used
# instead. Any newer Go (1.26.5+, 1.27+, or a future major) is accepted.
MIN_GO_MAJOR=1
MIN_GO_MINOR=26
MIN_GO_PATCH=5
MIN_GO_VERSION="${MIN_GO_MAJOR}.${MIN_GO_MINOR}.${MIN_GO_PATCH}"

# Minimum Node for building the frontend from source. Vite 7 requires Node
# >= 22.12 on the 22 LTS line, or the next even LTS line (24+). Odd/EOL majors
# (20, 21, 23) and early 22 patches (< 22.12, e.g. 22.3) are rejected, and the
# checksum-verified release fallback is used instead.
MIN_NODE_LTS_MAJOR=22
MIN_NODE_LTS_MINOR=12
MIN_NODE_NEXT_MAJOR=24
MIN_NODE_VERSION="${MIN_NODE_LTS_MAJOR}.${MIN_NODE_LTS_MINOR}+ (or ${MIN_NODE_NEXT_MAJOR}+)"

log() { printf '%s\n' "herdr-phone build: $*" >&2; }
fail() { log "error: $*"; exit 1; }

[ -f "$MANIFEST" ] || fail "manifest not found: $MANIFEST"
VERSION="$(sed -n 's/^version[[:space:]]*=[[:space:]]*"\([^"]*\)".*$/\1/p' "$MANIFEST" | head -n1)"
# Require strict semantic version X.Y.Z.
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  fail "manifest version is not a strict semantic version (X.Y.Z): '$VERSION'"
fi

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Linux) GOOS=linux ;;
  Darwin) GOOS=darwin ;;
  *) fail "unsupported OS: $os (herdr-phone v$VERSION targets macOS; Linux is supported only for building from source in development)" ;;
esac
case "$arch" in
  x86_64 | amd64) GOARCH=amd64 ;;
  arm64 | aarch64) GOARCH=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac

mkdir -p "$OUT_DIR"

# Prefer building from the exact checkout when compatible Go and Node exist.
go_compatible() {
  command -v go >/dev/null 2>&1 || return 1
  # Inspect the toolchain already on PATH. Without GOTOOLCHAIN=local, an older
  # Go can auto-download a newer toolchain merely while checking its version,
  # bypassing the smaller prebuilt-release fallback.
  gv="$(GOTOOLCHAIN=local go version 2>/dev/null | sed -n 's/.*go\([0-9][0-9]*\.[0-9][0-9]*\(\.[0-9][0-9]*\)\{0,1\}\).*/\1/p')"
  [ -n "$gv" ] || return 1
  # Split into major.minor.patch (patch defaults to 0 for an "X.Y" toolchain).
  gmajor="${gv%%.*}"
  grest="${gv#*.}"
  gminor="${grest%%.*}"
  case "$grest" in
    *.*) gpatch="${grest#*.}"; gpatch="${gpatch%%.*}" ;;
    *) gpatch=0 ;;
  esac
  # Accept anything strictly newer than the floor, and reject older 1.26 patches.
  [ "$gmajor" -gt "$MIN_GO_MAJOR" ] 2>/dev/null && return 0
  [ "$gmajor" -lt "$MIN_GO_MAJOR" ] 2>/dev/null && return 1
  [ "$gminor" -gt "$MIN_GO_MINOR" ] 2>/dev/null && return 0
  [ "$gminor" -lt "$MIN_GO_MINOR" ] 2>/dev/null && return 1
  [ "$gpatch" -ge "$MIN_GO_PATCH" ] 2>/dev/null && return 0
  return 1
}

node_compatible() {
  command -v node >/dev/null 2>&1 || return 1
  command -v npm >/dev/null 2>&1 || return 1
  # Parse major.minor (patch ignored). node --version prints e.g. "v22.23.1".
  nv="$(node --version 2>/dev/null | sed -n 's/^v\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')"
  [ -n "$nv" ] || nv="$(node --version 2>/dev/null | sed -n 's/^v\([0-9][0-9]*\).*/\1/p')"
  [ -n "$nv" ] || return 1
  nmajor="${nv%%.*}"
  case "$nv" in
    *.*) nminor="${nv#*.}"; nminor="${nminor%%.*}" ;;
    *) nminor=0 ;;
  esac
  # Accept the next even LTS line and later (24+)...
  [ "$nmajor" -ge "$MIN_NODE_NEXT_MAJOR" ] 2>/dev/null && return 0
  # ...or the 22 LTS line at 22.12 or newer (Vite 7 minimum).
  [ "$nmajor" -eq "$MIN_NODE_LTS_MAJOR" ] 2>/dev/null && [ "$nminor" -ge "$MIN_NODE_LTS_MINOR" ] 2>/dev/null && return 0
  return 1
}

if go_compatible && node_compatible; then
  log "building from source with $(go version) and node $(node --version)"
  [ -d "$WEB_DIR" ] || fail "frontend directory not found: $WEB_DIR"
  [ -f "$WEB_DIR/package-lock.json" ] || fail "web/package-lock.json is required for a reproducible install"
  log "installing frontend from the committed lockfile"
  ( cd "$WEB_DIR" && npm ci )
  log "building the embedded frontend (web/dist)"
  ( cd "$WEB_DIR" && npm run build )
  [ -d "$WEB_DIR/dist" ] || fail "frontend build did not produce web/dist"
  log "syncing the frontend into the Go embed directory"
  sh "$ROOT_DIR/scripts/embed-web.sh"
  log "asserting the production frontend is embedded (build gate)"
  ( cd "$ROOT_DIR" && GOTOOLCHAIN=local HERDR_PHONE_REQUIRE_WEB=1 go test ./internal/webui )
  log "building the Go binary with the frontend embedded"
  ( cd "$ROOT_DIR" && GOTOOLCHAIN=local CGO_ENABLED=0 go build -trimpath -o "$OUT_BIN" ./cmd/herdr-phone )
  log "built $OUT_BIN"
  exit 0
fi

log "no compatible Go $MIN_GO_VERSION+ and Node $MIN_NODE_VERSION toolchains; downloading release v$VERSION"

TMP=""
cleanup() { [ -n "$TMP" ] && rm -rf "$TMP"; }
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM
TMP="$(mktemp -d)"

archive="${BIN_NAME}_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
# The release base URL is overridable only to point the offline install smoke
# test at locally generated assets; it defaults to the published GitHub release.
# The SHA-256 checksum is verified regardless of source, so this cannot install
# an unverified binary.
base_url="${HERDR_PHONE_RELEASE_BASE_URL:-https://github.com/${REPO}/releases/download/v${VERSION}}"

download() { # url dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$1" -O "$2"
  else
    fail "curl or wget is required to download releases"
  fi
}

download "$base_url/$archive" "$TMP/$archive" || fail "could not download $archive (herdr-phone v$VERSION publishes macOS archives only)"
download "$base_url/checksums.txt" "$TMP/checksums.txt" || fail "could not download checksums.txt"

expected="$(grep " ${archive}\$" "$TMP/checksums.txt" | awk '{print $1}' | head -n1)"
[ -n "$expected" ] || fail "no checksum listed for $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$TMP/$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$TMP/$archive" | awk '{print $1}')"
else
  fail "sha256sum or shasum is required to verify the download"
fi

[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive (expected $expected, got $actual)"
log "checksum verified"

tar -xzf "$TMP/$archive" -C "$TMP"
[ -f "$TMP/$BIN_NAME" ] || fail "release archive did not contain $BIN_NAME"
mv "$TMP/$BIN_NAME" "$OUT_BIN"
chmod +x "$OUT_BIN"
log "installed $OUT_BIN from release v$VERSION"
