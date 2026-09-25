# Test Plan - local `.md` tickets + parallel `pie run`

Validates the `feat/local-parallel-run` work: `pie run file.md`, multiple files in
parallel, no Jira required, and plan/questions printed to the terminal.

> **Cost note.** Every `.md` (and every Jira key) spawns a **real `claude` session**.
> Keep test tickets tiny, and run the deterministic/edge tests (A, D) first - they cost
> nothing. `--dry-run` does edits + build but **never pushes or opens a PR**, so use it
> for everything until the final real-PR test (G).

## Prerequisites
- `claude` authenticated (`claude --version` works). Needed for any test that runs a session (B, C, E, F, G).
- `gh auth login` done - only needed for the real-PR test (G).
- Your Android repo cloned locally with a pushable `origin` (only matters for G).
- Built binary on this branch:

```bash
cd /path/to/your/pie/checkout
git checkout feat/local-parallel-run
go build -o pie . && go test ./... && echo "BUILD+TESTS OK"
```

## Isolated test home (don't pollute your real ~/.pie)

Run everything below in one shell so the env vars persist:

```bash
export PIE_HOME="$(mktemp -d)/.pie"; mkdir -p "$PIE_HOME"
echo "PIE_HOME=$PIE_HOME"
PIE=/path/to/your/pie/checkout/pie   # absolute path to the built binary
```

Point a config at **your** Android repo (adjust the path + Gradle commands):

```bash
cat > "$PIE_HOME/config.toml" <<'EOF'
jira_base_url = ""          # no Jira needed for local-file tests
concurrency   = 2           # start low; parallel Gradle builds share one cache (see risk note)

[[repo]]
path    = "/path/to/your/android-repo"
branch  = "ai/{ticket}-{slug}"
compile = "./gradlew :app:compileDebug"
test    = "./gradlew testDebugUnitTest"
max_retries = 2
EOF
```

---

## Test A - Edge cases & validation (no cost, no `claude`)

These fail/parse **before** any session starts, so they're free and instant.

```bash
$PIE run --help | head -2                      # usage shows <TICKET|FILE>...
$PIE run a.md b.md --title x ; echo "exit=$?"  # expect: error about --title + multiple args, exit=1
$PIE run FOO-1 --local       ; echo "exit=$?"  # expect: "--local requires --title", exit=1
printf '\n  \n' > /tmp/blank.md
$PIE run /tmp/blank.md       ; echo "exit=$?"  # expect: "could not derive a title", exit=1
$PIE run                     ; echo "exit=$?"  # expect: cobra "requires at least 1 arg(s)"
```

**Pass:** each prints the expected error and a non-zero exit; no `claude` session opens.

---

## Test B - One local `.md`, dry-run (first real session)

Write a tiny, safe ticket for your repo (pick something trivial - a comment, a constant,
a unit test). Example:

```bash
mkdir -p /tmp/mtickets
cat > /tmp/mtickets/tiny_change.md <<'EOF'
# Add a kdoc comment to the main Application class

Find the Application subclass and add a one-line KDoc comment above it
describing what the app does. No behavior change.
EOF

$PIE run /tmp/mtickets/tiny_change.md --dry-run
```

**Watch for / Pass criteria:**
- Log lines prefixed `[TINY-CHANGE]`.
- A worktree at `$PIE_HOME/worktrees/TINY-CHANGE`, a branch `ai/tiny-change-…`.
- The gate runs your real `compile`/`test`; ends at `gate green ✓` (or `needs-you` if it can't).
- **No push, no PR** (dry-run).

```bash
ls "$PIE_HOME/worktrees"                       # TINY-CHANGE
git -C "$PIE_HOME/worktrees/TINY-CHANGE" diff  # the actual change
$PIE status                                    # one row, state=review (or needs-you)
cat "$PIE_HOME/logs/TINY-CHANGE.log"           # full session log
```

---

## Test C - Multiple local `.md` in parallel, dry-run (the headline feature)

```bash
cat > /tmp/mtickets/a.md <<'EOF'
# Add a unit test for <some pure function>
Write one JUnit test asserting a known input/output. Domain layer only, no UI.
EOF
cat > /tmp/mtickets/b.md <<'EOF'
# Rename a private helper for clarity
Pick one private function with an unclear name and rename it + its references.
EOF

$PIE run /tmp/mtickets/a.md /tmp/mtickets/b.md --dry-run
```

**Pass criteria:**
- Both run concurrently (interleaved `[A]` / `[B]` lines; capped at `concurrency`).
- Two independent worktrees (`A`, `B`), two branches, two log files, two `status` rows.
- One ticket failing its gate does **not** stop the other.

```bash
ls "$PIE_HOME/worktrees"  # A  B
ls "$PIE_HOME/logs"       # A.log  B.log
$PIE status               # two rows
```

> If you have ≥3 small tickets, try all at once to see the `concurrency=2` cap hold
> (the 3rd waits for a slot). Bump `concurrency` in the config to compare.

---

## Test D - Filename → id derivation & collision dedup (cheap, dry-run optional)

```bash
mkdir -p /tmp/d1 /tmp/d2
printf '# Dup one\nbody\n' > /tmp/d1/dup.md
printf '# Dup two\nbody\n' > /tmp/d2/dup.md
$PIE run /tmp/d1/dup.md /tmp/d2/dup.md --dry-run
```

**Pass:** a `(warn) ticket id "DUP" collides; using "DUP-2"` line, and worktrees `DUP`
and `DUP-2` (no clobbering, no store primary-key collision).

---

## Test E - Plan + questions visible without Jira (minimal CLI validation)

Write a deliberately **under-specified** ticket so the agent asks questions:

```bash
cat > /tmp/mtickets/vague.md <<'EOF'
# Improve the settings screen
Make it better.
EOF

$PIE run /tmp/mtickets/vague.md --dry-run
```

**Pass criteria:**
- An `----- agent comment -----` block prints the **rendered plan and the questions**
  to the terminal (and into `$PIE_HOME/logs/VAGUE.log`) - they do **not** silently
  vanish.
- The run stops at `awaiting-answer`.
- You can answer by editing `vague.md` and re-running (same stop-and-re-run flow as Jira):

```bash
grep -A5 "agent comment" "$PIE_HOME/logs/VAGUE.log"
```

---

## Test F - Backward compatibility (only if you use Jira)

Confirm the original Jira path is unchanged. Set real `jira_base_url`/`jira_email` in the
config, export the token, then:

```bash
$PIE run YOUR-REAL-KEY --dry-run     # should fetch from Jira and behave exactly as before
$PIE run YOUR-KEY local.md --dry-run # mixed: Jira key + file in one invocation
```

**Pass:** the Jira key fetches/transitions as before; the file runs Jira-free; both isolated.

---

## Test G - The real thing: open an actual PR (no dry-run)

Only after B–C look right. Drop `--dry-run` on one tiny ticket:

```bash
$PIE run /tmp/mtickets/tiny_change.md
```

**Pass criteria:**
- Gate passes → commit → push → **PR opened** on `origin`.
- It stops at `review` (never auto-merges).
- `$PIE status` shows the PR URL.

Review and close/delete the PR + branch afterward so you don't leave test PRs around.

---

## Cleanup

```bash
# Remove the isolated test state entirely:
rm -rf "$(dirname "$PIE_HOME")"
# Delete any test branches the real run pushed:
git -C /path/to/your/android-repo branch -D ai/tiny-change-… 2>/dev/null
git -C /path/to/your/android-repo push origin --delete ai/tiny-change-… 2>/dev/null
```

---

## Known risk to watch (pre-existing, more likely now)

Apple Pie no longer runs Gradle itself - the agent discovers and runs the project's own
build inside its worktree (see `internal/emulator/emulator.go`, which now only manages the
AVD). It does not set `GRADLE_USER_HOME`, so every concurrent session shares the machine's
default `~/.gradle`. At `concurrency > 1` on a real Android repo you may therefore still
see Gradle lock contention or cache flakiness. If a parallel run fails on a Gradle lock but
each ticket passes when run alone, that's this - not the new code. Mitigation: keep
`concurrency` low, or run heavy tickets one at a time.

## Quick pass/fail checklist

- [ ] A: all validation errors fire before any session (free)
- [ ] B: single `.md` → worktree + branch + diff + gate, no PR (dry-run)
- [ ] C: two `.md` run in parallel, isolated, capped at `concurrency`
- [ ] D: same-basename files → `DUP` + `DUP-2`, dedup warning
- [ ] E: plan + questions printed to terminal/log without Jira; stops at `awaiting-answer`
- [ ] F: Jira key still works; mixed key + file works (if you use Jira)
- [ ] G: real run opens a PR and stops at `review`
