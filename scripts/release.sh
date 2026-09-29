#!/usr/bin/env bash
# Cut a release from your machine, obfuscated, authenticated with the token
# `gh` already holds — no PAT juggling, no CI round-trip.
#
#   scripts/release.sh v0.2.0                 # tag, push, obfuscated release
#   scripts/release.sh v0.2.0 --dry-run       # build+package locally, publish nothing
#   scripts/release.sh v0.2.0 --no-obfuscate  # skip garble (fast, for testing)
#   scripts/release.sh --snapshot             # untagged local snapshot into ./dist
#
# What it does for a real release:
#   1. `gh auth token`  -> GITHUB_TOKEN for goreleaser's publish step
#   2. validate semver, clean tree, tag does not already exist
#   3. create + push an annotated tag (goreleaser derives the version from it)
#   4. `goreleaser release --clean` builds darwin/linux x amd64/arm64 through
#      scripts/garble.sh (max obfuscation) and uploads the GitHub Release.
#
# Requires: gh (logged in), goreleaser, go, and garble (unless --no-obfuscate).
set -euo pipefail

die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[36m==>\033[0m %s\n' "$*"; }

version=""
snapshot=0
dry_run=0
obfuscate=1

for arg in "$@"; do
	case "$arg" in
		--snapshot)     snapshot=1 ;;
		--dry-run)      dry_run=1 ;;
		--no-obfuscate) obfuscate=0 ;;
		-h|--help)      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		--*)            die "unknown flag: $arg" ;;
		*)              [ -z "$version" ] || die "unexpected argument: $arg"; version="$arg" ;;
	esac
done

# --- preflight -------------------------------------------------------------
command -v go         >/dev/null 2>&1 || die "go not found"
command -v goreleaser >/dev/null 2>&1 || die "goreleaser not found — https://goreleaser.com/install/"
command -v gh         >/dev/null 2>&1 || die "gh not found — https://cli.github.com"

if [ "$obfuscate" = "1" ]; then
	command -v garble >/dev/null 2>&1 || die "garble not found — 'go install mvdan.cc/garble@latest' (or pass --no-obfuscate)"
	# garble patches the linker source; Go rejects that on a toolchain it
	# downloaded into GOMODCACHE. Catch it here instead of 2 minutes into the
	# build. See RELEASE.md for the real-SDK setup.
	case "$(go env GOROOT)" in
		*pkg/mod/golang.org/toolchain*)
			die "go's GOROOT is a downloaded toolchain ($(go env GOROOT)).
    garble needs a real SDK. Install one and put it first on PATH:
        go install golang.org/dl/go1.27.1@latest && go1.27.1 download
        export PATH=\"\$HOME/sdk/go1.27.1/bin:\$PATH\"
    (garble's minimum Go can be newer than go.mod's - see RELEASE.md.)" ;;
	esac
	export PIE_NO_OBFUSCATE=0
	info "obfuscation: ON (garble -literals -tiny -seed=random)"
else
	export PIE_NO_OBFUSCATE=1
	info "obfuscation: OFF (--no-obfuscate)"
fi

# gh's token is what authenticates goreleaser's GitHub publish. This is the
# whole point of the script: reuse the login you already have.
GITHUB_TOKEN="$(gh auth token 2>/dev/null || true)"
[ -n "$GITHUB_TOKEN" ] || die "no gh token — run 'gh auth login' first"
export GITHUB_TOKEN

# POSTHOG_API_KEY is optional: absent = telemetry compiled out (see .goreleaser.yaml).
export POSTHOG_API_KEY="${POSTHOG_API_KEY:-}"

# --- snapshot: no tag, no publish -----------------------------------------
if [ "$snapshot" = "1" ]; then
	info "goreleaser snapshot (no tag, no publish) -> ./dist"
	goreleaser release --snapshot --clean
	info "done — artifacts in ./dist"
	exit 0
fi

# --- real release ----------------------------------------------------------
[ -n "$version" ] || die "usage: scripts/release.sh vX.Y.Z [--dry-run] [--no-obfuscate]"
echo "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' \
	|| die "version must be semver like v0.2.0 (got '$version')"

# Dry run builds locally and publishes nothing, so it doesn't need a clean tree
# or a fresh tag — that's exactly when you want to test with local changes.
if [ "$dry_run" = "1" ]; then
	info "dry run: local build+package only, nothing tagged or published"
	# --snapshot avoids needing a tag; --skip=publish keeps it entirely local.
	goreleaser release --snapshot --clean --skip=publish
	info "dry run done — inspect ./dist"
	exit 0
fi

[ -z "$(git status --porcelain)" ] || die "working tree is dirty — commit or stash first"
git rev-parse -q --verify "refs/tags/$version" >/dev/null && die "tag $version already exists locally"
git ls-remote --exit-code --tags origin "refs/tags/$version" >/dev/null 2>&1 \
	&& die "tag $version already exists on origin"

branch="$(git rev-parse --abbrev-ref HEAD)"
info "releasing $version from '$branch' @ $(git rev-parse --short HEAD)"

info "tagging $version and pushing to origin"
git tag -a "$version" -m "Release $version"
git push origin "$version"

info "goreleaser release --clean (obfuscated, publishing to GitHub)"
if ! goreleaser release --clean; then
	die "goreleaser failed. The tag was pushed; after fixing, delete + re-tag:
    git push origin :$version && git tag -d $version"
fi

info "released $version — https://github.com/Apple-Pie-AI/pie-tui/releases/tag/$version"
