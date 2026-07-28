

<p align="center">
  <img src="docs/assets/apple_pie_logo.png" alt="Apple Pie" width="160">
</p>

<p align="center"><em>A native mobile app harness that drives agentic CLIs to run enterprise mobile development cycles.</em></p>







<p align="center"><a href="https://github.com/Apple-Pie-AI/pie-tui/releases/latest"><img src="https://img.shields.io/github/v/release/Apple-Pie-AI/pie-tui?label=release&color=success" alt="Latest release"></a> <a href="https://github.com/Apple-Pie-AI/pie-tui/releases"><img src="https://img.shields.io/github/downloads/Apple-Pie-AI/pie-tui/total" alt="Downloads"></a> <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-blue" alt="Platform: macOS and Linux"> <a href="LICENSE"><img src="https://img.shields.io/badge/license-proprietary-lightgrey" alt="License: proprietary"></a></p>

<p align="center"><a href="https://apple-pie.io">apple-pie.io</a> · New here? Start with <a href="#first-steps">First steps</a>.</p>

You already use an agentic CLI to work tickets by hand: plan the change, make it, run the build and tests, open the PR, repeat. Apple Pie runs that loop for you. Each ticket goes through plan → implement → verify in its own git worktree and comes out the other side as a Pull Request. Run many in parallel, watch them all from one control pane, and get pulled in only when an agent is genuinely stuck. Our own PR counts roughly doubled; now we mostly just review.

https://github.com/user-attachments/assets/3803f102-ad33-466d-ae3d-34b92c121281



## What ships today

| Area | Shipped today | Where it's headed |
|------|---------------|-------------------|
| **Agent CLI** | Claude Code | Codex and other agentic CLIs behind the same harness |
| **Platform** | Android: Gradle builds, emulator verification, Android Studio handoff | iOS and the rest of the mobile stack |
| **Tickets** | Jira keys and local markdown files | — |
| **Review** | GitHub Pull Requests via `gh` | — |

The left column is real and exercised end to end. The right column is where we're headed: **iOS support is under active development, with a preview release coming soon.**

## How it works

| Stage | What it does |
|-------|--------------|
| **Plan** | Reads the ticket and your codebase, and writes a plan you can review before any code is written |
| **Implement** | Makes the change, then reviews and fixes its own work before handing it over |
| **Verify** | Runs your project's own build and tests — booting the emulator when a ticket needs it — and only goes green after seeing them pass |

Verification is never faked: the build and tests that run are your project's own, including company build setups. No green, no PR — the ticket lands in **NEEDS YOU** instead. And what you get is a clean branch with just the change: Apple Pie's own working files never reach your PRs.

## Install

macOS and Linux. Windows is not supported.

Grab the binary for your platform from the [latest release](https://github.com/Apple-Pie-AI/pie-tui/releases/latest):

```bash
# macOS (Apple Silicon)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_darwin_arm64

# macOS (Intel)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_darwin_amd64

# Linux (x86-64)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_linux_amd64

# Linux (arm64)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_linux_arm64

# then, whichever one you downloaded:
chmod +x pie && sudo mv pie /usr/local/bin/
pie --version
```

Versioned archives (`.zip` for macOS, `.tar.gz` for Linux) and a SHA-256 `checksums.txt` are attached to every release. macOS binaries are signed for direct use — `curl` downloads run as-is, but if you download through a browser, clear the quarantine flag with `xattr -d com.apple.quarantine pie`.

Then `pie init` to configure your repo.

| Prerequisite | Why | Check with |
|---|---|---|
| [Claude Code](https://claude.ai/download), authenticated | The agent that works your tickets | `claude --version` |
| `git` | Worktrees, branches, commits | `git --version` |
| [`gh`](https://cli.github.com), authenticated | Opens and tracks pull requests | `gh auth status` |
| [Android Studio](https://developer.android.com/studio) | Only for tickets that need an emulator | `pie doctor` |
| Jira | Optional — local `.md` tickets work without it | `pie doctor` |

Apple Pie works with your own `git`, `gh`, and `claude` — same binaries, same auth, same config you already use. No separate accounts, no extra setup.

## First steps

Never run this before? Do these eight steps in order. Nothing here touches your repo or opens a PR until step 8.

**1. Check your prerequisites.**

```bash
claude --version && gh auth status && git --version
```

Claude Code has to have been launched and signed in at least once. `gh` should say *Logged in*. Android Studio only matters for tickets that need an emulator, so skip it for now.

**2. Install, then confirm the binary works.**

```bash
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_darwin_arm64   # pick your platform — see Install above
chmod +x pie && sudo mv pie /usr/local/bin/
pie --help
```

**Check:** `pie --help` prints the command list.

**3. Configure it against your Android repo.**

```bash
cd ~/path/to/your/android-repo
pie init
```

The wizard asks, in order: **repo path** (pre-filled with your current directory — accept it), **branch pattern** (accept `{ticket}-{slug}`), **plan review gate** (say **yes** for your first few runs — it's the single best beginner default), **three optional models** (leave all blank to use Claude Code's defaults), **Anthropic API key** (optional — leave blank to use your logged-in `claude` session; if you do enter one it goes to the OS keychain, never the config file), and **telemetry consent**.

**Check:** `ls -l ~/.pie/config.toml` shows a `0600` file.

**4. Run the connectivity check.**

```bash
pie doctor
```

**Check:** the three required checks — git origin, `claude`, `gh` — pass. The optional emulator checks can fail for now; they only matter once a ticket needs instrumented tests.

**5. Write your first ticket as a markdown file.**

```bash
cat > ~/first-ticket.md <<'EOF'
# Add a TODO comment to the main Activity

Add a single `// TODO: hello from Apple Pie` comment at the top of the
app's main Activity class. Nothing else.
EOF
```

The ticket id comes from the **filename**: uppercased, with every run of non-alphanumeric characters collapsed to a dash. So `first-ticket.md` becomes `FIRST-TICKET`, and that id names the worktree, the log, and the branch.

**6. Run it — safely.**

```bash
pie run ~/first-ticket.md --dry-run --review-plan
```

Five things make this safe to try: no Jira account is involved; `--dry-run` skips push and PR entirely, so nothing reaches GitHub; `--review-plan` stops the run *before a single line of code is written*; all work happens in `~/.pie/worktrees/FIRST-TICKET`, not in your checkout; and the change itself is one comment line.

**Check:** run `git status` in your own repo. It's untouched.

**7. Watch it from the dashboard.**

```bash
pie
```

Bare `pie` opens the TUI. Tickets are grouped under **NEEDS YOU**, **RUNNING**, **READY FOR REVIEW**, and **STOPPED**; the footer lists the keys. Press <kbd>enter</kbd> on your ticket to read the full plan, approve it, and watch it work through to READY FOR REVIEW.

**Check:**

```bash
cat ~/.pie/plans/FIRST-TICKET.md              # the plan it wrote
git -C ~/.pie/worktrees/FIRST-TICKET diff     # the change it made
pie logs FIRST-TICKET                         # the whole session
```

**8. Do it for real.** Drop `--dry-run` on a second local ticket to get an actual pull request, or connect Jira and run `pie run PROJ-123`.

Whichever you pick, the guarantee is the same: **it stops at `review`. Apple Pie never merges anything.** When you're done experimenting, press <kbd>x</kbd> on a ticket in the TUI to stop it and remove its worktree.

## Using it day to day

Bare `pie` is the dashboard, and it's where most of the work happens: write or paste a ticket, confirm its id and branch name, press enter. Queue several and they run in parallel.

| Key | Action |
|---|---|
| <kbd>↑</kbd> <kbd>↓</kbd> | Move |
| <kbd>enter</kbd> | Choose — on a paused ticket, read its plan |
| <kbd>n</kbd> | Start new ticket(s) |
| <kbd>a</kbd> | Answer a blocked ticket |
| <kbd>o</kbd> | Open the worktree in Android Studio |
| <kbd>c</kbd> | Open the session in Claude Code |
| <kbd>R</kbd> | Resume from the last stage |
| <kbd>x</kbd> | Stop and clean up |
| <kbd>:</kbd> | Command palette — everything above, plus doctor, config, and housekeeping |

Everything is available from the CLI too:

```bash
pie run PROJ-123              # one ticket → PR
pie run PROJ-123 PROJ-124     # two tickets in parallel
pie run a.md b.md c.md        # local markdown tickets, in parallel

pie doctor                    # check git, claude, and gh connectivity
pie logs PROJ-123             # print the session log for a ticket
pie status                    # table of all sessions (--watch to follow)
pie start                     # background housekeeping (--once for a single pass)
pie stop                      # stop it
pie --splash                  # replay the title screen
```

`pie start` is housekeeping, not a scheduler — it cleans up after tickets whose PRs you've merged or closed, and shuts the emulator down when it's been idle. Tickets are always started by you, from `pie run` or the TUI.

### Every `pie run` flag

| Flag | What it does |
|---|---|
| `--dry-run` | Everything except `git push` and the PR. Try this first |
| `--review-plan` | Pause after planning so you can approve or redirect before any code is written |
| `--branch <name>` | Exact branch name for the PR, overriding the config pattern. One ticket at a time |
| `--base <ref>` | Base branch — or another ticket's id — to stack on. The PR targets it |
| `--repo <path>` | Which configured repo to use. Defaults to the first one |
| `--resume` | Keep your manual fixes in the existing worktree, re-run verification, then PR |
| `--ship` | Skip verification entirely: commit, push, and PR from your manual fix |
| `--from-plan` | Skip planning and implement from the plan already in the worktree |
| `--local` | Skip Jira and take the ticket from `--title`/`--desc` |
| `--title` / `--desc` | The ticket summary and body, with `--local` |
| `--splash` | Replay the title screen first. Works on any command |

`--base` accepts a ticket id as well as a branch name, so `pie run PROJ-124 --base PROJ-123` builds on the branch PROJ-123 created and opens its PR against it. A plain re-run of a stacked ticket keeps its base rather than silently resetting to the default branch.

## Staying in control

Apple Pie is built to hand tickets back rather than plough through them. Three mechanisms do that.

**The plan review gate.** Turn it on per ticket when queueing, with `--review-plan`, or as your default with `review_plans` in the config. The run pauses after planning; you read the plan in the TUI's full-screen viewer and either approve it or send feedback for a re-plan. Every plan is archived to `~/.pie/plans/<ticket>.md` regardless, so you can read it after the fact with **View Plan**.

**NEEDS YOU.** An ambiguous ticket, a compile error the agent can't fix, or a verification that never went green all land here rather than being forced through. Answer in the TUI, resume the Claude session, or open the worktree in Android Studio and fix it by hand — then `--resume` to re-verify, or `--ship` to trust your fix and PR straight away.

**It stops at `review`.** There is no auto-merge, no merge flag, and no code path anywhere in the product that merges a pull request.

One rule worth knowing: blocking questions always win over the plan review gate — if the plan has questions, you'll be asked them even with the gate off.

## Why Android-native matters

General ticket→PR agents don't know what verifying an Android change means. Apple Pie does:

- **Emulator as a shared resource.** The SDK and AVD are auto-detected, and parallel agents take turns on one AVD — no two agents fight over a device.
- **Instrumented vs. unit test routing.** Decided at plan time, enforced at verify time.
- **Worktree → Android Studio handoff.** One keystroke opens any agent's worktree in the IDE.
- **Screenshot tickets.** Drag images into the terminal; the agent sees them while planning.

## Configuration

Everything Apple Pie owns lives in one directory, `~/.pie`: your settings in `config.toml` (mode `0600`), one worktree per ticket under `worktrees/`, archived plans under `plans/`, session logs under `logs/`, and editable PR-body and Jira-comment templates under `templates/`. Delete the directory and Apple Pie is gone.

The keys you'll actually touch in `config.toml`:

| Key | Default | |
|---|---|---|
| `[[repo]] path` | — | Your Android project |
| `[[repo]] branch` | `{ticket}-{slug}` | Branch pattern. `{ticket}` is the lowercased id, `{slug}` the kebab-cased title |
| `[[repo]] base` | the repo's default branch | What PRs target |
| `review_plans` | `false` | Pause every ticket for plan review |
| `model_plan` / `model_impl` / `model_review` | blank | Per-stage models. Blank uses Claude Code's default |
| `max_budget_usd` | `5` | Per-ticket ceiling passed to `claude` |

Secrets never go in the config file — they live in your OS keychain, or in `PIE_JIRA_TOKEN` / `PIE_ANTHROPIC_TOKEN` / `PIE_GIT_TOKEN` for headless use. `PIE_HOME` relocates the whole directory; `PIE_NO_SPLASH=1` and `PIE_NO_SOUND=1` quiet the title screen.

## Cost, telemetry, and privacy

Apple Pie runs on your existing Claude Code subscription or API key. There's no separate account and no markup. Each ticket costs a full working session's worth of tokens, so five parallel tickets is roughly five concurrent sessions. Start with one.

Telemetry is opt-in, asked once during `pie init`. When enabled it sends three events — `run_started`, `run_completed`, `tui_opened` — carrying a randomly generated device id, your OS and architecture, the Apple Pie version, and for a completed run its outcome state, duration in seconds, and whether it errored. **No ticket content, no file paths, no repo or branch names, no code.** Turn it off any time with `telemetry_enabled = false` in `~/.pie/config.toml`.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `pie: command not found` | The directory you installed to isn't on your PATH — `/usr/local/bin` usually is |
| A run fails immediately | `pie doctor` — it's almost always `gh` or `claude` auth |
| A ticket is stuck or vanished | `pie logs <TICKET>` has the full session. `pie status` shows every state |
| A ticket landed in NEEDS YOU | Read the reason in the TUI, fix it in `~/.pie/worktrees/<TICKET>`, then `pie run <TICKET> --resume` |
| Verification skips instrumented tests | `pie doctor` — the emulator probably isn't configured, so it fell back to unit tests |
| A worktree is wedged | Press <kbd>x</kbd> in the TUI to stop and clean up, then re-run |
| macOS blocks the binary | You downloaded through a browser — `xattr -d com.apple.quarantine /usr/local/bin/pie` |

## Why we built it

Our teams are measured by PRs merged. We ran Claude Code agents in parallel with git worktrees to keep up, and ended up supervising every one of them: terminals, branches, plan mode, the emulator, PR descriptions. Apple Pie automates that toil.

## Uninstall

```bash
pie stop 2>/dev/null                              # stop background housekeeping if it's running
sudo rm /usr/local/bin/pie                        # remove the binary
rm -rf ~/.pie                                     # config, state, logs, worktrees
security delete-generic-password -s pie 2>/dev/null   # macOS: any stored keys
```

Everything Apple Pie writes lives under `~/.pie`, so that's the whole of it.

## License

Apple Pie is closed-source, distributed as binaries only. Your use of the
binaries is governed by the end-user license in [LICENSE](LICENSE).
© 2026 apple-pie.io — all rights reserved.
