# Configuration

Everything Apple Pie reads lives in `~/.pie/config.toml`, written by `pie init` at mode `0600`. You can edit it by hand, re-run `pie init` to regenerate it, or use **Edit config** in the TUI (<kbd>:</kbd> → Edit config) for the common fields.

Set `PIE_HOME` to relocate the whole directory — config, state, worktrees, and logs all move with it.

## A minimal config

```toml
review_plans = true

[[repo]]
path   = "/Users/you/src/android-app"
branch = "{ticket}-{slug}"
```

That's genuinely enough. Every other key has a working default.

## Repositories

Repos are a TOML array of tables, so you can register more than one and pick between them with `pie run --repo <path>`. Without `--repo`, the first entry wins.

```toml
[[repo]]
path   = "~/src/android-app"
branch = "{ticket}-{slug}"
base   = "develop"
```

| Key | Default | |
|---|---|---|
| `path` | — | Path to the project. `~` is expanded |
| `branch` | `{ticket}-{slug}` | Branch name pattern — see below |
| `base` | the repo's default branch | What new branches fork from and what PRs target |

### Branch patterns

Two placeholders are substituted:

- `{ticket}` — the ticket id, lowercased (`PROJ-123` → `proj-123`)
- `{slug}` — the ticket title, kebab-cased and capped at 40 characters

For `PROJ-123 — "Add pull to refresh"`:

| Pattern | Result |
|---|---|
| `{ticket}-{slug}` | `proj-123-add-pull-to-refresh` |
| `feature/{ticket}` | `feature/proj-123` |
| `{ticket}/{slug}` | `proj-123/add-pull-to-refresh` |

`pie run --branch <name>` overrides the pattern for a single ticket. Once a branch name is recorded for a ticket, re-runs and `--resume` reuse it rather than re-deriving it, so a hand-picked name survives.

`pie run --from-branch <name>` works ON an existing branch instead: the worktree checks the branch out as-is (never resets it), which is how a ticket addresses feedback on an open PR. The TUI's new-ticket wizard offers the same choice; picking an existing branch skips the ticket-id step entirely (the branch itself becomes the ticket's identity - there's nothing left to name). Shipping pushes to the same branch; before ever creating a PR, Apple Pie checks whether the branch already has one and reuses its URL instead of opening a duplicate.

## The agent

```toml
model_plan        = ""
model_impl        = ""
model_review      = ""
model_verify      = ""
model_comment_fix = ""
max_budget_usd    = 5
review_plans      = false

# optional per-stage budgets (blank/0 = max_budget_usd)
max_budget_plan_usd        = 0
max_budget_impl_usd        = 0
max_budget_review_usd      = 0
max_budget_verify_usd      = 0
max_budget_comment_fix_usd = 0
allowed_tools     = "…"
```

| Key | Default | |
|---|---|---|
| `model_plan` | blank | Model for the planning stage |
| `model_impl` | blank | Model for implementation |
| `model_review` | blank | Model for the adversarial self-review |
| `model_verify` | blank | Model for the verify stage. Blank falls back to `model_impl` |
| `model_comment_fix` | blank | Model that fixes PR review comments (and their revise rounds). Blank falls back to `model_impl` |
| `saved_models` | `[]` | Models added with **Add a model…** on the hub's **Edit models** screen, offered in every stage's picker. Any name `--model` accepts |
| `max_budget_usd` | `5` | The default `claude --max-budget-usd` for one run of a stage. When a stage reaches it the ticket pauses and asks in the dashboard — see [Stage budgets](#stage-budgets) |
| `max_budget_plan_usd` | `0` | The planning stage's own budget. `0` = `max_budget_usd` |
| `max_budget_impl_usd` | `0` | Implementation, its self-review fix round, and change-review rework rounds. `0` = `max_budget_usd` |
| `max_budget_review_usd` | `0` | The self-review stage. `0` = 30% of `max_budget_usd` |
| `max_budget_verify_usd` | `0` | The verify stage. `0` = `max_budget_usd` |
| `max_budget_comment_fix_usd` | `0` | PR review-comment fixes and their revise rounds. `0` = `max_budget_usd` |
| `review_plans` | `false` | Default answer for the plan review gate. `pie run --review-plan` turns it on per ticket |
| `review_before_pr` | `true` | Pause after a green verify so you review the change in the hub before any PR is created. From the change screen you can annotate files, revert them to base, send feedback for a rework round, or approve — approving re-verifies only if the change moved since the park, then commits, pushes, and opens the PR. `--review-change=false` (or the config set to `false`) restores the old auto-PR flow; the explicit ship verbs `--ship` and `--resume` bypass the gate deliberately |
| `allowed_tools` | see below | The `--allowed-tools` allowlist for the implement, verify, and comment-fix stages |
| `approval_policy` | `"auto"` | The permission callback's auto-answer: `auto` approves commands already on the allowlist without prompting; `always-ask` routes every callback to the dashboard. See [permissions.md](permissions.md) |

**Models are deliberately blank by default.** A blank value makes Apple Pie omit `--model` entirely, so Claude Code uses whatever default you or your organization configured — including enterprise policy, and including non-Claude models on Bedrock or Vertex. Set any identifier your account allows (`haiku`, `sonnet`, `opus`, or a full model name). Nothing is hardcoded.

**The hub's Edit models screen picks from a list.** Each stage offers, in order: its default; the `/model` list your company curates in Claude Code's own settings (`modelPicker`, read from the managed settings file and `~/.claude/settings.json`, never from a project checkout); the `opus`/`sonnet`/`haiku` aliases, unless that list replaces Claude Code's built-in lineup; your `saved_models`; and **Add a model…** for any other name. A picked model is checked once in the background with a one-turn `claude` run: a name Claude Code refuses fails in seconds at no cost, one it accepts costs a fraction of a cent to a few cents, and the stage shows the model that actually ran. A list Claude Code fetches from a server isn't on disk, so it can't be shown; the aliases, saved models, and the check still apply.

**A stage's model also selects its permission gate** when `permissions` is unset: models that support permission auto-mode run with the classifier (no allowlist), everything else runs under the `allowed_tools` allowlist. Changing a per-stage model — including `model_comment_fix` — therefore changes how that stage is gated; set `permissions = "allowlist"` (or `"auto"`) explicitly to pin the gate regardless of model. See [permissions.md](permissions.md).

### `allowed_tools`

The default is a scoped allowlist — file edits, the build/test commands an Android verify actually needs, and read-only inspection — rather than arbitrary shell access:

```
Edit Write Read Glob Grep MultiEdit TodoWrite Skill
Bash(./gradlew:*) Bash(gradle:*) Bash(cd:*) Bash(chmod +x:*)
Bash(adb:*) Bash(java:*) Bash(mkdir:*) Bash(which:*) Bash(pwd:*) Bash(echo:*)
Bash(ls:*) Bash(cat:*) Bash(head:*) Bash(tail:*) Bash(tee:*)
Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(git status:*)
Bash(git diff:*) Bash(git log:*) Bash(git show:*)
```

Prefer `extra_allowed_tools` for additions (append-only, keeps tracking default upgrades); replace `allowed_tools` wholesale only if you know you want to stop receiving the defaults. Commands outside the list are no longer hard-refused — they pause for your approval in the dashboard (see [permissions.md](permissions.md)). The planning and self-review stages use their own narrower, non-configurable allowlists (planning is read-only apart from writing its plan; self-review can only read the diff).

Caveat worth knowing: read commands like `cat` can still reach paths outside the worktree. The allowlist stops destructive and network commands, not all reads. For real filesystem confinement, turn the sandbox on.

### Stage budgets

Every stage run is one `claude -p` call with `--max-budget-usd` set to that stage's budget. When a session reaches it, the ticket does **not** fail. It pauses, and the question appears in the dashboard the same way a permission prompt does: a `⏸` banner (`1 budget question waiting`), the `approve? ⏸` badge on the ticket, and an overlay showing what the stage spent:

```
  PROJ-123 reached its budget
  the agent is paused, its work kept - continuing resumes the same session

    The implement stage reached its $5.00 budget ($5.02 spent so far). Continue with another $5.00?

  Continue - grant the same budget again
  Stop - park the ticket at needs-you
```

- **Continue** resumes the *same* Claude session with a fresh budget of the same size, so the agent keeps its context and work. If it runs out again, you're asked again.
- **Stop** parks the ticket under NEEDS YOU with a message naming the budget and the key to raise, never "the ticket is ambiguous".
- Like permission prompts, the question never times out. Stopping the run expires it.

**An agent that quits early counts too.** Claude Code shows the agent its budget and what's left of it on every turn, and agents ration against it: one implement session spent its $5 reading code, cut scope as the money ran down, and reported `needs_human` with $0.02 left and no code written. Apple Pie counters that two ways:

- Every stage prompt tells the agent the limit isn't its to manage: never cut scope or stop early because of budget.
- A stage that ends **unfinished** (plan: no fresh `plan.json`; implement and verify: no fresh report, or one saying `needs_human`; comment fixes: nothing produced) after spending **90% or more** of its budget is treated as a budget stop and asks the same question ("…stopped without finishing near its $5.00 budget…"). Continue resumes the session and tells it to finish everything it skipped. A cheap `needs_human` is left alone, because that's a real blocker.

Budgets apply per stage run, not per ticket: a ticket that plans, implements and verifies can spend up to the sum of those stages' budgets without being asked. A run started with `pie run` from a shell asks the same way, so open `pie` to answer it. Tune them in **Edit config** or with the `max_budget_*_usd` keys above. `make simulate-budget` replays the whole flow live against the real CLI (a few cents on haiku; `STAGE=impl` targets the implement stage, `MODE=stop` answers Stop); `make repro-budget` checks the CLI behavior it depends on.

## Review comment write-back

```toml
review_reply   = true
review_resolve = true
```

| Key | Default | |
|---|---|---|
| `review_reply` | `true` | After a comment-fix run, reply "Fixed in `<sha>`" on each review thread it addressed |
| `review_resolve` | `true` | Also mark those threads resolved on GitHub |

These gate what Apple Pie writes back to the PR when you approve a batch of review fixes (`--ship-comments`, spawned by the dashboard's approve bar). Every reply is previewed in the approve screen first — the agent drafts it during the local fix, you can rewrite it there, and what ships is exactly what the preview showed. Only inline threads are replied to — a review's summary body has no reply target — and a thread is never replied to twice. Set `review_resolve = false` on teams where marking a thread settled is the reviewer's call, not the author's; the reply is welcome either way. Failures here are logged but never fail the run: by the time write-back happens, the fix is already committed and pushed.

## Android

```toml
avd_name              = "Pixel_7_API_34"
android_sdk_path      = "/Users/you/Library/Android/sdk"
emulator_idle_timeout = 30
```

| Key | Default | |
|---|---|---|
| `avd_name` | blank | The AVD to boot for tickets needing instrumented tests |
| `android_sdk_path` | `$ANDROID_HOME`, else `$ANDROID_SDK_ROOT` | SDK root. Falls back to the platform default location |
| `emulator_idle_timeout` | `30` | Minutes of inactivity before the cleanup daemon shuts the emulator down |

**If `avd_name` is unset, tickets that want an emulator fall back to unit tests** rather than failing. `pie doctor` tells you which case you're in, and validates the name against `emulator -list-avds`.

Only one AVD is used, shared across parallel tickets by a SQLite-backed semaphore — agents queue for it rather than racing.

## Jira

```toml
jira_base_url = "https://yourcompany.atlassian.net"
jira_email    = "you@yourcompany.com"
```

Both blank by default, which is fine — local `.md` tickets don't need Jira. The API token is *not* stored here; see [Secrets](#secrets) and [docs/jira.md](jira.md).

With Jira configured, Apple Pie reads the issue summary and description, and posts the plan and outcome back as comments.

## Daemon

```toml
poll_interval = 30
concurrency   = 3
```

| Key | Default | |
|---|---|---|
| `poll_interval` | `30` | Seconds between cleanup passes in `pie start` |
| `concurrency` | `3` | **Read but not currently enforced** — see below |

`concurrency` is loaded and defaulted but nothing acts on it yet. Real parallelism today comes from two places: `pie run A B C` runs one goroutine per ticket in a single process, and the TUI launches a separate detached `pie run` per ticket. Neither is capped. Treat the setting as reserved.

## Sandbox

```toml
[sandbox]
enabled         = false
allowed_domains = ["nexus.internal.example.com"]
allow_write     = ["~/.m2"]
```

Controls Claude Code's OS sandbox for the `claude` processes Apple Pie spawns.

**Default is `enabled = false`**, which turns the sandbox *off* for Apple Pie's runs. That's deliberate: Gradle needs network access and writes to `~/.gradle`, and Apple Pie's guardrail is the scoped `allowed_tools` allowlist instead. On a shared or CI machine, set `enabled = true` — Apple Pie then keeps the sandbox on and merges these Gradle-friendly defaults with whatever you add:

| | Built in when enabled |
|---|---|
| Domains | `repo.maven.apache.org`, `dl.google.com`, `*.gradle.org` |
| Writable | `~/.gradle`, `~/.konan`, `~/.android`, plus `android_sdk_path` |

`allowed_domains` and `allow_write` extend those lists; they don't replace them.

## Telemetry

```toml
telemetry_enabled = false
device_id         = "…"
```

| Key | Default | |
|---|---|---|
| `telemetry_enabled` | unset — you're asked once | `false` disables it completely |
| `device_id` | generated at consent | A random UUID. Delete it to reset your identity |

Three events are sent when enabled: `run_started`, `run_completed`, `tui_opened`. Each carries the device id, your OS and architecture, and the Apple Pie version; `run_completed` adds the outcome state, duration in seconds, and whether it errored. No ticket content, file paths, repo names, branch names, or code is transmitted.

## Environment variables

| Variable | |
|---|---|
| `PIE_HOME` | Relocate `~/.pie` entirely — config, state, worktrees, logs |
| `PIE_JIRA_TOKEN` | Jira API token. **Takes precedence over the keychain** |
| `PIE_ANTHROPIC_TOKEN` | Anthropic API key. Takes precedence over the keychain |
| `PIE_GIT_TOKEN` | Git token. Takes precedence over the keychain |
| `PIE_NO_SPLASH` | Any non-empty value suppresses the first-run title screen |
| `PIE_NO_SOUND` | Any non-empty value mutes the splash audio |
| `ANDROID_HOME` | Default for `android_sdk_path`, and SDK auto-detection |
| `ANDROID_SDK_ROOT` | Fallback when `ANDROID_HOME` is unset |
| `VISUAL` / `EDITOR` | Editor opened by <kbd>ctrl+e</kbd> in the TUI. Falls back to `vim` |

## Secrets

Tokens are never written to `config.toml`. They're resolved in this order:

1. **`PIE_<KEY>_TOKEN`** — `PIE_JIRA_TOKEN`, `PIE_ANTHROPIC_TOKEN`, `PIE_GIT_TOKEN`
2. **The macOS keychain**, under service `pie` with the account name `jira`, `anthropic`, or `git`

`pie init` writes to the keychain via the `security` CLI — no cgo, no third-party dependency. Inspect or remove entries yourself:

```bash
security find-generic-password -s pie -a jira -w      # read
security delete-generic-password -s pie               # remove all of Apple Pie's
```

On Linux, and anywhere else without a keychain backend, use the environment variables. Writing a secret will report an error rather than silently pretending it saved.

Most people never set an Anthropic key at all — leaving it blank uses your logged-in `claude` session.

## Files Apple Pie writes

```
~/.pie/
├── config.toml                  settings (0600)
├── state.db                     SQLite: sessions and emulator state (WAL mode)
├── worktrees/<TICKET>/          one git worktree per ticket
├── plans/<TICKET>.md            archived plan, rendered
├── plans/<TICKET>.json          archived plan, raw
├── reports/<TICKET>.json        what the agent did and its verification verdict
├── reports/<TICKET>-review.json the self-review verdict
├── logs/<TICKET>.log            full session log
├── templates/pr_body.tmpl       PR body template — edit to taste
├── templates/jira_comment.tmpl  Jira comment template
├── templates/plan_comment.tmpl  plan comment template
├── pasted/<ID>/                 tickets written in the TUI, plus any images
├── daemon.pid                   single-instance guard for `pie start`
├── daemon.log                   daemon output
└── splash-seen                  so the title screen plays once
```

Templates are only written if missing, so your edits survive `pie init`.
