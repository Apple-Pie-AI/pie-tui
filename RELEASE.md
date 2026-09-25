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

garble needs a **real Go SDK** whose `GOROOT` is a normal directory (matching
`go.mod`, currently 1.26.2). Do **not** rely on `GOTOOLCHAIN` to fetch the
toolchain on demand — garble patches the linker source, and Go forbids that on
the toolchain it downloads into `GOMODCACHE` (you'll get
`overlay ... must not be replaced`). If your default `go` is older:

```bash
go install golang.org/dl/go1.26.2@latest
go1.26.2 download                          # installs a real SDK under ~/sdk/go1.26.2
export PATH="$HOME/sdk/go1.26.2/bin:$PATH" # make it the `go` on PATH (no GOTOOLCHAIN)
go install mvdan.cc/garble@latest          # build garble against that same SDK
```

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
`main` checkout (the script refuses a dirty tree). `garble` installs to
`~/go/bin`, which is not on PATH by default - `export PATH="$HOME/go/bin:$PATH"`
or the script dies with `garble not found`.

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
2. `scripts/release.sh vX.Y.Z` from a clean `main`.
3. `releases/latest` returns the new tag; installer gives the new version.

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
telemetry compiled out).

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
