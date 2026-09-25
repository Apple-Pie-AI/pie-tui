# Test quality: state, plan, and handoff

Working document for continuing the test-hardening work started on
`feature/refactor-name-pie`. Written for a fresh agent with no prior context.

---

## 0. Read this first — environment

**`go.mod` requires Go 1.26.2; the machine has 1.23.4 with `GOTOOLCHAIN=local`.**
Every `go` command must be prefixed or the toolchain refuses to run:

```bash
GOTOOLCHAIN=auto go build ./...
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go vet ./...
```

Without it you get `go: go.mod requires go >= 1.26.2 (running go 1.23.4)`.
The editor/LSP shows this as a persistent diagnostic on every file; it is
environmental noise, not a code error.

`make test` exists and runs `go test ./...`, but **no CI runs it** —
`.github/workflows/` contains only `release.yml`. Nothing enforces the suite.

---

## 1. What has already been done

Commit `2eabf6c` — *fix: harden destructive deletes, ship gate, keychain and pids*
(25 files, +1039/−80). It closed ten defects found by an audit of the test suite.
Read the commit body first; it explains each defect and why the fix is shaped the
way it is. Summary of the code that changed:

| Area | Change |
|---|---|
| `internal/paths` | `ValidTicketID`, `ticketElem` sanitizer, `UnderWorktrees`; all ticket-path helpers route through the sanitizer |
| `internal/git` | `parseStaleWorktreePath` scans to the last quote; `os.RemoveAll(stale)` gated by `paths.UnderWorktrees` |
| `internal/runner` | `finishShip` records `StateFailed` before returning it; verify stage uses `ReadReportSince` |
| `internal/agent` | `resolveAgentFile`, `ReadReportSince`, `ErrStaleReport` |
| `internal/secrets` | `Set` errors off darwin; secret on stdin (written twice) with `Setsid`; `goos` var for testability |
| `internal/vcs` | `PRState` uses `Output()` with stderr kept separate |
| `cmd` | `runArg` helper (5 call sites); `processAlive` deleted in favour of `pidAlive`; `readPid` trims; `isPieProcess` guard on `stop`; `doctor` delegates to `build.ResolvePaths`; both `secrets.Set` callers surface the error |

Coverage went 35.1% → 42.6%. Per package now: `paths` 73.1, `git` 69.9,
`config` 66.2, `store` 60.2, `secrets` 58.8, `vcs` 46.3, `cmd` 37.4,
`build` 37.9, `jira` 30.9, `agent` 24.4, `runner` 15.1.

---

## 2. What is NOT verified — start here

**The tool has never been run with this code.** All ten fixes were verified at
unit level only. The changes touch worktree creation, the PR-ship path, and the
verify gate — none of which has been exercised end to end.

**Task 1 (highest priority): one real `--dry-run` run.**
`--dry-run` does edits and build but never pushes or opens a PR, so it is safe.

```bash
export PIE_HOME="$(mktemp -d)/.pie"; mkdir -p "$PIE_HOME"
GOTOOLCHAIN=auto go build -o /tmp/pie .
printf '# Tiny change\n\nAdd a comment to the top of README.\n' > /tmp/tiny.md
/tmp/pie run /tmp/tiny.md --dry-run
```

Confirm specifically, because these are what changed:

1. the worktree is created under `$PIE_HOME/worktrees/TINY/` (exercises
   `ticketElem` + `CreateWorktree`);
2. the run reaches `review` state and reports `verified` correctly (exercises
   `ReadReportSince` — a false stale-rejection would stop it at `needs-you`,
   which would be a regression introduced by the mtime guard);
3. `pie init` in a real terminal saves the Anthropic key **without hanging**
   (exercises the `Setsid` fix — see the landmine in §5);
4. `pie doctor` does not report a broken emulator setup on a machine where
   runs work (exercises the `build.ResolvePaths` delegation).

`TEST_PLAN.md` in the repo root is a fuller manual QA script; note it costs real
`claude` sessions per ticket, so keep test tickets tiny.

---

## 3. The real problem, still unsolved

Coverage is not uniformly thin — it is **~100% of the pure helpers and ~0% of
everything that spawns a process, opens a socket, or deletes a directory.**

There is **no exec seam and no `httptest` server anywhere in the repo.** That one
missing piece of infrastructure — not difficulty — is why every `git` / `gh` /
`claude` / `adb` / `emulator` / `security` / `osascript` path and the entire Jira
client sit near zero. Adding unit tests around the edges will not change this.

**Task 2: build the two harnesses.** Everything in §4 gets much cheaper after.

- **Fake binary on PATH.** Write a shell script into `t.TempDir()` that records
  its `argv` to a file and exits with a scripted code, prepend that dir to
  `$PATH`, and assert on the recorded argv. This makes every `exec.Command` call
  site testable without running the real tool.
- **`httptest` server** for `internal/jira` — the client already takes a base
  URL, so this needs no refactor at all.

---

## 4. Prioritized remaining work

Ranked by (production blast radius × how cheap the test is).

### Tier 1 — cheap, no refactor needed

1. **`internal/jira` (30.9%)** — the single cheapest large win. Stand up
   `httptest` and cover `FetchIssue` (URL/fields construction, key escaping,
   non-200 → error, status extraction, description reaching `flattenDesc`),
   `AddComment` (payload shape, 4xx handling) and `Client.auth` (basic vs
   bearer). `FetchIssue` is the sole source of the ticket text the agent plans
   from; `AddComment` is the only feedback channel to the human.

2. **`store` emulator CAS state machine (0%)** — `RegisterEmulator`,
   `SetEmulatorBooting`, `AcquireEmulator`, `ReleaseEmulator`, `SetEmulatorIdle`.
   These have the same claim-if-unclaimed semantics that `Claim` earned a
   10-goroutine race test for (`TestClaimIsAtomic` in `internal/store` — copy
   that pattern; it is the best test in the repo). A dropped
   `WHERE` guard drives one emulator from two tickets; a broken `Release` wedges
   every later ticket in `runner`'s unbounded 5s acquire loop forever.

3. **`config.Resolve` (0%)** — four paths: no repos; empty `repoPath` → first
   repo; `~`-expanded match; not-found error. It chooses which repository the
   agent commits and opens a PR against, so silently returning `Repos[0]` when a
   specific `--repo` did not match points the whole run at the wrong codebase.
   Also assert `applyDefaults`' actual values — especially that `AllowedTools`
   grants `Bash` only for `./gradlew` and read-only commands. That string is
   handed to `claude --allowed-tools`; if it ever gains a bare `Bash`, an
   autonomous agent gets arbitrary shell and no test fails.

4. **`agent.parseStream` (0%)** — feed synthetic stream-json through its
   `io.Reader`: session_id from a normal line, absent on malformed JSON, a line
   over the 16MB scanner cap, a non-string id. The returned `sessionID` is the
   only thing that lets the retry, review-fix, verify and "Open in Claude Code"
   stages reattach to the same conversation. It swallows every unmarshal and
   scanner error, so a silent empty return means every stage starts a
   context-free session and burns budget with no visible failure.

### Tier 2 — needs the harnesses from §3

5. **Subprocess argv construction** — nothing in the repo observes an argv
   anywhere. Extract and characterize `hub.launchRun` (batched-vs-per-ticket,
   `--base`/`--branch`/`--review-plan` assembly), `doAction`'s per-action flag,
   and `openClaude`'s auto-chain plus `shellQuote`/`asEscapeAS` double-escaping.
   A dropped `--review-plan` opens a PR without the human gate the user asked
   for; a wrong `--base` targets the wrong branch.

6. **`internal/runner` (15.1%)** — the orchestration pipeline itself.
   `finishShip`, `verifyWithAgent`, `shipOnly`, `bootOrWait`, `stageImages` are
   all 0%, and no test ever constructs a `store.Store`, so branch precedence,
   stacked-base resolution and emulator arbitration are entirely dark. Highest
   value first: extract `finishShip`'s base-branch precedence
   (`t.Base` → `store.BaseBranch` → `repo.Base` → `git.DefaultBranch`) into a
   pure helper and table-test it, then cover the `--dry-run` short-circuit
   (commits, never pushes or opens a PR). A regression there means `--dry-run`
   force-pushes and opens real PRs — the exact thing the flag promises not to do.

7. **`internal/daemon` (no test files)** — `cleanupResolved` is the only code in
   the repo that destroys agent output (`git worktree remove --force` discards
   uncommitted work). Cover the guard chain behind an injected PR-state lookup:
   `OPEN`, empty, lowercase, and a `vcs.PRState` error must all be incapable of
   reaching `git.RemoveWorktree`. Note the guard chain is currently *correct* and
   errs toward not deleting — this is about keeping it that way.

### Tier 3 — infrastructure

8. **CI.** Add `.github/workflows/ci.yml` running `GOTOOLCHAIN=auto go test ./...`
   and `go vet ./...` on push and PR. Without it nothing enforces any of the
   above, and the tests added in `2eabf6c` can rot silently. This is ~15 lines
   and arguably belongs before everything in Tier 2.

9. **`cmd/localsource_test.go` is not gofmt-clean** and was already that way
   before this work — left alone deliberately to keep the diff focused. Easy
   cleanup whenever someone is in that file.

---

## 5. Landmines discovered — do not rediscover these the hard way

**A green test here can coexist with a broken binary.** This happened during the
work above and is the single most important thing to carry forward.

- **`security -w` reads `/dev/tty`, not stdin.** `security add-generic-password
  ... -U -w` prompts via `readpassphrase(3)`, which opens `/dev/tty` whenever a
  controlling terminal exists and only falls back to stdin when it cannot. A
  first attempt at the keychain fix piped the secret on stdin, passed its own
  test, and would have hung `pie init` forever on every real invocation — because
  `go test` is tty-less and takes the working path. Fixed with
  `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}`. It also prompts
  **twice**; feeding the value once leaves the second read at EOF and silently
  stores an **empty** password. To reproduce tty behaviour, allocate a real pty
  (`pty.fork()` in Python) — `script -q /dev/null` will not work if the harness
  itself has no tty.

- **Package-level cobra flag vars leak between tests in `cmd`.** `runTitle`,
  `runLocal` et al. persist across tests in the same binary; a test that calls
  `resolveSpecs` will inherit whatever the previously-run test left there. Use
  `resetRunFlags(t)` in `cmd/run_test.go`.

- **Tests were reading the developer's real `~/.pie`.** `TestMain` in `cmd` and
  `internal/vcs` now forces `PIE_HOME` to a temp dir. Keep that when adding
  packages that touch `paths.*` or render templates.

- **An arg that exists as a file takes the local-`.md` branch by design.** Do not
  use a real path like `/etc/passwd` as a "hostile ticket id" test case — it goes
  down the legitimate local-ticket path, not the traversal path.

---

## 6. Standard of proof expected

The audit that started this found four existing tests that passed on a gutted
implementation (`TestReloadGrouping` keyed its want map on `"DONE"`, a label
`newGroups` never emits; `TestPlanReviewNotActive` passed if `IsActive` were
`return false`; and two more). All are fixed. Do not add more of them.

**Mutation-check every new test**: revert the fix, confirm the test fails,
restore. If a test still passes against the broken code, it is not a test. This
was done for the path containment, `readPid` trimming, `IsActive`, and
`ReadReportSince` tests in `2eabf6c`, and it is what caught the value of each.

Two further rules:

- **No skipped tests.** `GOTOOLCHAIN=auto go test ./... -v | grep -c '^--- SKIP'`
  is currently `0`. A `t.Skip` on `runtime.GOOS` means the branch never executes
  anywhere — this project's CI and its developers are all on macOS. Indirect the
  platform behind a var instead (see `goos` in `internal/secrets`).
- **Do not chase the coverage number.** It is a symptom, not the goal. A test
  that raises coverage without being able to fail is worse than no test.

---

## 7. Honest assessment of where this stands

Eight of the ten items in `2eabf6c` were real defects with concrete failure
modes and they are genuinely gone. Two framings in the original write-up were
inflated and should not be repeated: the ticket-id traversal was described as
"critical", but for a single-user local dev tool `pie run ../../..` is a
footgun rather than an attack — the only person who can trigger it is the person
running it. Same for the `ps` argv exposure: real, but it needs a shared machine.

What did **not** improve is the shape of the suite. The defects were fixed; the
reason they survived — no exec seam, no HTTP seam, no CI — was not. That is what
§3 and Tier 3 are for, and it is worth more than any further individual test.
