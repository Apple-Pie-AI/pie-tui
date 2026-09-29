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
# `prod` needs a real go SDK (garble can't use GOTOOLCHAIN downloads, Decision 18)
# new enough for go.mod and garble. This script puts the newest one it finds -
# your `go`, or ~/sdk/go* from golang.org/dl - on PATH for you. See RELEASE.md.
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

# ── toolchain: the newest real SDK that satisfies go.mod ────────────────────
# Newest, not an exact go.mod match: garble only supports recent Go releases,
# so its minimum is usually above go.mod's (garble v0.18.0 needs 1.27+).
want_go="$(awk '/^go /{print $2; exit}' go.mod)"   # e.g. 1.26.2
version_ge() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" = "$2" ]; }
resolve_sdk() {
  local gobin info root v best="" best_v=""
  for gobin in "$(command -v go 2>/dev/null || true)" "$HOME"/sdk/go*/bin/go; do
    [ -x "$gobin" ] || continue
    # Outside the module and GOTOOLCHAIN=local, so go reports its own SDK
    # instead of switching to (or refusing for) the version go.mod names.
    info="$(cd / && GOTOOLCHAIN=local "$gobin" env GOROOT GOVERSION 2>/dev/null)" || continue
    root="$(printf '%s\n' "$info" | sed -n 1p)"
    v="$(printf '%s\n' "$info" | sed -n 2p | sed 's/^go//')"
    case "$root" in *pkg/mod/golang.org/toolchain*) continue ;; esac   # downloaded, not real
    [ -n "$v" ] && version_ge "$v" "$want_go" || continue
    if [ -z "$best_v" ] || version_ge "$v" "$best_v"; then best="$root/bin" best_v="$v"; fi
  done
  [ -n "$best" ] || return 1
  PATH="$best:$PATH"; export PATH
}

if resolve_sdk; then
  export GOTOOLCHAIN=local
elif [ "$MODE" = prod ]; then
  die "prod build needs a real Go SDK >= ${want_go} that garble supports, but none was found.
  install one:  go install golang.org/dl/go1.27.1@latest && go1.27.1 download
  (garble can't use the GOTOOLCHAIN auto-download path — Decision 18; see RELEASE.md.)"
else
  export GOTOOLCHAIN=auto   # test build may auto-download the toolchain
  echo "note: no Go SDK >= ${want_go} found; GOTOOLCHAIN=auto will fetch one for the test build." >&2
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
