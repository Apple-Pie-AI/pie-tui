# Releasing Apple Pie

Releases are cut **locally** with `scripts/release.sh`, which runs GoReleaser
authenticated by the token `gh` already holds (`gh auth token`). Every published
binary is obfuscated with [garble](https://github.com/burrowers/garble) at the
maximum setting (Decision 18): identifier renaming, encrypted literals, and a
stripped symbol table with no readable stack traces.

## One-time setup

```bash
brew install goreleaser gh          # or see https://goreleaser.com/install/
gh auth login                       # the release script reuses this token
```

garble needs a **real Go SDK** whose `GOROOT` is a normal directory. Do **not**
rely on `GOTOOLCHAIN` to fetch the toolchain on demand — garble patches the
linker source, and Go forbids that on the toolchain it downloads into
`GOMODCACHE` (you'll get `overlay ... must not be replaced`).

The SDK has to satisfy two minimums: `go.mod`'s (currently 1.26.2) and
garble's own. garble only supports recent Go releases, and its minimum is
usually the higher of the two — **garble v0.18.0 requires Go 1.27 or newer**,
and fails the build within a second otherwise:

```
Go version "go1.26.4" is too old; please upgrade to go1.27.0 or newer
```

So the release SDK is currently Go 1.27.1, not the `go.mod` version:

```bash
go install golang.org/dl/go1.27.1@latest
go1.27.1 download                          # installs a real SDK under ~/sdk/go1.27.1
export PATH="$HOME/sdk/go1.27.1/bin:$PATH" # make it the `go` on PATH (no GOTOOLCHAIN)
go install mvdan.cc/garble@latest          # build garble against that same SDK
```

When a newer garble raises its minimum again, install the SDK version the
error names and update this section.

### The PostHog key

Telemetry only works in a release built with `POSTHOG_API_KEY` set. Without
it, the build succeeds and telemetry is silently compiled out (v0.5.1 and
v0.6.0 shipped that way). Keep the key in the macOS Keychain, never in the
repo, a dotfile, or your shell history; the release command below reads it
from there. Store it once, typing the key at the prompt:

```bash
security add-generic-password -U -a "$USER" -s apple-pie-posthog-api-key \
  -l "Apple Pie release: PostHog project API key" -w
```

It's a PostHog project key (`phc_…`): write-only, and compiled into every
binary we ship, so it isn't a secret the way a token is. It still stays out of
git so it can be rotated in one place. To rotate: create a new key in PostHog,
rerun the command above with it, and update the `POSTHOG_API_KEY` repo secret
(see CI fallback).

## Cutting a release

```bash
scripts/release.sh v0.2.0
```

That validates the version, checks the tree is clean, creates and pushes an
annotated `v0.2.0` tag, then runs `goreleaser release --clean` to build
darwin/linux × arm64/amd64 (obfuscated) and publish the GitHub Release directly
on this repo — archives, bare binaries, and `checksums.txt`. `install.sh`
resolves `releases/latest` on this same repo, so the release is live the
moment this step finishes; there is no second repo to mirror to.

Pick versions with [semver](https://semver.org): `v0.1.0`, `v1.0.0`, etc.

Before running the script: merge the PR into `main` and release from a clean
`main` checkout (the script refuses a dirty tree). Put both the release SDK and
`~/go/bin` (where `garble` installs) first on PATH, or the script dies with
`garble not found` or garble's "Go version ... is too old":

```bash
export PATH="$HOME/sdk/go1.27.1/bin:$HOME/go/bin:$PATH"
go version                                 # must print go1.27.1, not your default go
POSTHOG_API_KEY="$(security find-generic-password -a "$USER" -s apple-pie-posthog-api-key -w)" \
  scripts/release.sh v0.2.0 2>&1 | sed -E 's/(telemetry\.APIKey=)[^ ]*/\1<redacted>/'
```

The `sed` keeps the key out of the output: GoReleaser prints its build flags,
key included. The obfuscated build of all four targets can take over 10 minutes.

If goreleaser's upload step fails (GitHub's upload endpoint has done this -
`upload failed ... POST https://uploads.github.com/...`, then a draft release
with a couple of assets), do **not** re-tag: the tag and the binaries in
`./dist` are good. Finish the release by hand instead:

```bash
cd dist
gh release upload v0.2.0 -R Apple-Pie-AI/pie-tui --clobber \
  checksums.txt pie_0.2.0_darwin_amd64.zip pie_0.2.0_darwin_arm64.zip \
  pie_0.2.0_linux_amd64.tar.gz pie_0.2.0_linux_arm64.tar.gz \
  pie_darwin_amd64 pie_darwin_arm64 pie_linux_amd64 pie_linux_arm64
gh release edit v0.2.0 -R Apple-Pie-AI/pie-tui --draft=false --latest
```

Then verify exactly what a user's installer does:

```bash
curl -fsSL https://api.github.com/repos/Apple-Pie-AI/pie-tui/releases/latest | grep tag_name   # the new tag
curl -fsSL https://raw.githubusercontent.com/Apple-Pie-AI/pie-tui/main/install.sh | bash
pie --version
```

## Release checklist

1. PR merged into `main`; `go test ./...` and `make integration` green.
2. The PostHog key is in the Keychain: `security find-generic-password -a "$USER" -s apple-pie-posthog-api-key -w | wc -c` prints a non-zero length.
3. `scripts/release.sh vX.Y.Z` from a clean `main`, with `POSTHOG_API_KEY` set as above.
4. `releases/latest` returns the new tag; installer gives the new version.

## Re-releasing the same tag (e.g. to fix a bad release)

```bash
git push origin :v0.2.0   # delete from remote
git tag -d v0.2.0          # delete locally
scripts/release.sh v0.2.0  # re-tag at current HEAD and re-release
```

## About the obfuscation

`scripts/garble.sh` is the wrapper GoReleaser calls as its `go` binary; it runs
`garble -literals -tiny -seed=random`. Trade-off to know: `-tiny` strips
file:line metadata, so panics and PostHog crash telemetry lose stack detail —
this is intentional ("no easy traces"). To build/debug with normal traces, set
`PIE_NO_OBFUSCATE=1` (that's what `make snapshot` does) or pass `--no-obfuscate`.

garble does source-level obfuscation (names, literals, symbols); it is **not** a
packer and does not flatten control flow. UPX-style packing is deliberately not
used — it breaks the macOS ad-hoc codesign step and is trivially reversible.
Since the source is public, obfuscation no longer hides implementation details
from anyone willing to read the repo — its remaining purpose is just to keep
release binaries free of readable stack traces and identifier names.

## CI fallback (manual)

`.github/workflows/release.yml` runs the same GoReleaser pipeline but is
`workflow_dispatch` only — trigger it from the Actions tab with an existing tag.
It is **not** wired to tag pushes, because that would race the local script for
the same release. It needs a `POSTHOG_API_KEY` repo secret (optional; absent =
telemetry compiled out). Set it from the Keychain, so the key is never typed
or printed:

```bash
security find-generic-password -a "$USER" -s apple-pie-posthog-api-key -w | tr -d '\n' \
  | gh secret set POSTHOG_API_KEY --repo Apple-Pie-AI/pie-tui
```

## Homebrew tap (not yet active)

The `.goreleaser.yaml` has the Homebrew config commented out. To enable it:

1. Create the `Apple-Pie-AI/homebrew-pie` repo on GitHub. (The tap repo keeps
   the old name; only the formula and binary are called `pie`.)
2. Uncomment the `brews:` section in `.goreleaser.yaml`.
3. Add a `HOMEBREW_TAP_GITHUB_TOKEN` secret (or, for the local script, ensure
   your `gh` token has write access to `homebrew-pie`).
4. Cut a new release — GoReleaser will push the formula automatically.

Users can then install with:

```bash
brew install apple-pie-ai/pie/pie
```
