#!/usr/bin/env bash
# Build the `pie` binary in one of two shapes, for one or more targets.
#
#   scripts/build.sh test   → plain `go build`    → dist/test/<os>_<arch>/pie
#   scripts/build.sh prod   → obfuscated `garble` → dist/prod/<os>_<arch>/pie   (Decision 18)
#
# Default target is darwin/arm64 (Apple Silicon). Add more with flags:
#   --linux         also build linux/amd64 + linux/arm64
#   --darwin-amd64  also build darwin/amd64 (Intel Mac)
#   --windows       coming soon — not yet buildable (unix-only syscalls); skipped
#   --all           every buildable target
#   --clean         wipe the mode's dist dir first
#   --version V     override the version string (default: git describe)
#
# `prod` needs a real go SDK matching go.mod on PATH, because garble can't use
# the GOTOOLCHAIN auto-download path (Decision 18). This script finds one via
# the `goX.Y.Z` golang.org/dl shim or ~/sdk/goX.Y.Z and puts it on PATH for you.
set -euo pipefail

# ── config (edit these to change defaults) ──────────────────────────────────
MODULE="github.com/Apple-Pie-AI/pie-tui"
DIST_ROOT="dist"
DEFAULT_TARGETS=(darwin/arm64)          # what builds when no target flags given
GARBLE_FLAGS=(-literals -tiny -seed=random)   # prod obfuscation (Decision 18)
# ────────────────────────────────────────────────────────────────────────────

cd "$(dirname "$0")/.."   # repo root

usage() { sed -n '2,17p' "$0" | sed 's/^#\( \|$\)//'; }
die()   { echo "error: $*" >&2; exit 1; }

MODE="test"
WANT_LINUX=0 WANT_WIN=0 WANT_DARWIN_AMD64=0 WANT_ALL=0 CLEAN=0
VERSION="${VERSION:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    test|prod)      MODE="$1" ;;
    --linux)        WANT_LINUX=1 ;;
    --darwin-amd64) WANT_DARWIN_AMD64=1 ;;
    --windows)      WANT_WIN=1 ;;
    --all)          WANT_ALL=1 ;;
    --clean)        CLEAN=1 ;;
    --version)      shift; VERSION="${1:-}" ;;
    -h|--help)      usage; exit 0 ;;
    *)              die "unknown argument: $1 (try -h)" ;;
  esac
  shift
done

# ── assemble the target list ────────────────────────────────────────────────
declare -a TARGETS=()
if [ "$WANT_ALL" = 1 ]; then
  TARGETS=(darwin/arm64 darwin/amd64 linux/amd64 linux/arm64)
  WANT_WIN=1   # surface the "coming soon" note under --all
else
  TARGETS=("${DEFAULT_TARGETS[@]}")
  [ "$WANT_DARWIN_AMD64" = 1 ] && TARGETS+=(darwin/amd64)
  [ "$WANT_LINUX" = 1 ] && TARGETS+=(linux/amd64 linux/arm64)
fi

if [ "$WANT_WIN" = 1 ]; then
  echo "note: windows is coming soon — not yet buildable (unix-only syscalls in" >&2
  echo "      internal/proc, agent, emulator, secrets); skipping windows targets." >&2
fi
[ "${#TARGETS[@]}" -gt 0 ] || die "no buildable targets selected"

# ── toolchain: garble (prod) needs a real SDK matching go.mod on PATH ────────
want_go="$(awk '/^go /{print $2; exit}' go.mod)"   # e.g. 1.26.2
resolve_sdk() {
  go version 2>/dev/null | grep -q "go${want_go} " && return 0
  if command -v "go${want_go}" >/dev/null 2>&1; then
    local r; r="$("go${want_go}" env GOROOT 2>/dev/null || true)"
    [ -n "$r" ] && [ -x "$r/bin/go" ] && { PATH="$r/bin:$PATH"; export PATH; return 0; }
  fi
  [ -x "$HOME/sdk/go${want_go}/bin/go" ] && { PATH="$HOME/sdk/go${want_go}/bin:$PATH"; export PATH; return 0; }
  return 1
}

if resolve_sdk; then
  export GOTOOLCHAIN=local
elif [ "$MODE" = prod ]; then
  die "prod build needs a go${want_go} SDK, but none was found.
  install it:  go install golang.org/dl/go${want_go}@latest && go${want_go} download
  (garble can't use the GOTOOLCHAIN auto-download path — Decision 18.)"
else
  export GOTOOLCHAIN=auto   # test build may auto-download the toolchain
  echo "note: no go${want_go} SDK found; GOTOOLCHAIN=auto will fetch it for the test build." >&2
fi

[ "$MODE" = prod ] && ! command -v garble >/dev/null 2>&1 && \
  die "garble not found. install it:  go install mvdan.cc/garble@latest"

# ── ldflags (mirror the Makefile) ───────────────────────────────────────────
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
POSTHOG_API_KEY="${POSTHOG_API_KEY:-}"
LDFLAGS="-s -w -X ${MODULE}/cmd.version=${VERSION} -X ${MODULE}/internal/telemetry.APIKey=${POSTHOG_API_KEY}"

DIST="${DIST_ROOT}/${MODE}"
[ "$CLEAN" = 1 ] && rm -rf "$DIST"
mkdir -p "$DIST"

echo "▸ mode=${MODE}  version=${VERSION}  go=$(go version | awk '{print $3}')  →  ${DIST}/"
[ "$MODE" = prod ] && echo "▸ garble ${GARBLE_FLAGS[*]}"
echo "▸ targets: ${TARGETS[*]}"

for t in "${TARGETS[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  out="${DIST}/${os}_${arch}/pie"
  mkdir -p "$(dirname "$out")"
  echo "  → ${t}"
  if [ "$MODE" = prod ]; then
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      garble "${GARBLE_FLAGS[@]}" build -ldflags "$LDFLAGS" -o "$out" .
  else
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -ldflags "$LDFLAGS" -o "$out" .
  fi
  # Ad-hoc codesign darwin Mach-O so Gatekeeper doesn't kill it (matches the
  # goreleaser post-hook). Skipped when codesign is unavailable (e.g. Linux host).
  if [ "$os" = darwin ] && command -v codesign >/dev/null 2>&1; then
    codesign --sign - --force "$out" >/dev/null 2>&1 || echo "    warn: codesign failed for $out" >&2
  fi
  echo "    ✓ ${out}  ($(du -h "$out" | cut -f1))"
done

echo "done."
