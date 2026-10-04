<p align="center">
  <img src="docs/assets/apple_pie_logo.png" alt="Apple Pie" width="160">
</p>

<p align="center"><strong>A harness for Android Development</strong><br><em>Run everyday Android dev workflows with coding agents, each in its own git worktree, and tracking them all from one control pane.</em></p>

<p align="center"><a href="https://github.com/Apple-Pie-AI/pie-tui/releases/latest"><img src="https://img.shields.io/github/v/release/Apple-Pie-AI/pie-tui?label=release&color=success" alt="Latest release"></a> <a href="https://github.com/Apple-Pie-AI/pie-tui/releases"><img src="https://img.shields.io/github/downloads/Apple-Pie-AI/pie-tui/total" alt="Downloads"></a> <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-blue" alt="Platform: macOS and Linux"> <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="License: MIT"></a></p>

<p align="center">New here? Start with <a href="#first-steps">First steps</a>.</p>

Apple Pie use your current Claude Code subscription to run the workflows Android developers repeat every day like turning a ticket into a pull request, answering review comments and reworking a change. Every workflow runs in its own git worktree, so several can run side by side without touching your checkout. A single control pane shows what each agent is doing and pulls you in only at the moments that need a developer.

## Start now

macOS and Linux. Windows is not supported.

```bash
curl -fsSL https://raw.githubusercontent.com/Apple-Pie-AI/pie-tui/main/install.sh | bash
```

<p align="center">
  <img src="https://github.com/user-attachments/assets/8c55bba6-e774-4d1f-8b27-d34b6423e788" alt="apple-pie-review-mobile" width="720">
</p>

## Everything in one control pane

From the apple pie dashboard you:

- **From ticket to PR:** Give Apple Pie a ticket, review and iterate on the plan, then approve it to implement, verify, and open a PR following your branch and workflow conventions.
- **Handle review comments** Review and resolve GitHub comments without leaving Apple Pie. See each comment alongside its diff, choose which ones to address, and approve the fixes and replies before they're posted.
- **Review the diff** Review every changed file before anything is committed. Ask for changes directly in chat, right next to the code you're reviewing.
- **Take over when you want to** Open any worktree in Android Studio or jump directly into the agent's Claude Code session whenever you want to take control.
- **Choose the right model for each stage** Configure different models for planning, implementation, verification, self-review, and review-comment fixes, without manually switching between them.

The agents are in different stages in the dashboard:

* **NEEDS YOU** — a plan to approve, questions to answer, changes to review, comment fixes to preview, or a run that got stuck.
* **RUNNING** — agents planning, implementing, or verifying.
* **PR READY FOR REVIEW** — open pull requests. Moved to **NEEDS YOU** when the Pull Request has open comments.
* **CLOSED** — when a Pull Request is merged or closed, it appears here. The worktree is cleaned up.
* **STOPPED** — when you decide to stop an agent from the TUI or CLI. The worktree is cleaned up.

Agents keep moving until they hit a decision that actually needs a developer; then the row moves to NEEDS YOU. Everything is <kbd>↑</kbd><kbd>↓</kbd> to move, <kbd>enter</kbd> to act, <kbd>esc</kbd> to back out; <kbd>enter</kbd> on a row opens its menu. 

See it in action solving PR comments:

<p align="center">
  <img src="https://github.com/user-attachments/assets/1e985616-3179-40a1-b797-8779d789fc35" alt="apple-pie-review-mobile" width="720">
</p>

## You stay in control

- **Isolated worktrees.** Every agent works in `~/.pie/worktrees/<ticket>`. Your main checkout isn't modified.
- **Plans can require approval.** Turn on the plan gate and nothing is written until you approve the plan.
- **Changes require approval.** By default, before anything is committed or pushed, you see every changed file and its diff.
- **Commands can require approval.** Anything outside your allowlist pauses the agent and asks you on the dashboard. Your Claude Code `ask`/`deny` rules still apply.
- **Builds must actually pass.** No fresh, successful verification, no PR. The one way around it is `--ship`, a human override for a fix you've checked yourself.
- **Review-comment fixes are previewed.** You see each fix's diff and the reply before anything is pushed or posted.
- **Nothing merges itself.** There is no auto-merge, no merge flag, and no code path that merges a pull request.
- **Apple Pie's own files never reach your PRs.** Plans, reports, and logs live under `~/.pie`; the agent's scratch directory is git-excluded and scrubbed before every commit.

How each workflow runs, every gate, and the full state machine are in [docs/workflows.md](docs/workflows.md).

## Built for Android

General coding agents don't know what verifying an Android change means. Apple Pie's stages do:

- **Your build, not a guess.** The agent discovers how *your* project builds and tests — Gradle tasks, multi-module and KMP projects, company build scripts — runs them for real, and self-certifies the result in a report Apple Pie checks for freshness, so a crashed run can't leave a stale "it passed" behind.
- **Unit vs. instrumented tests.** Decided at plan time, enforced at verify time.
- **Emulator coordination.** The SDK and AVD are auto-detected, and parallel agents take turns on the emulator through a shared lock, so no two agents fight over a device. The background daemon shuts it down when it's been idle.
- **Android Studio handoff.** Open any agent's worktree in the IDE, and your hand-edits there count: the change is re-verified before it ships.

## Install

macOS and Linux. Windows is not supported.

```bash
curl -fsSL https://raw.githubusercontent.com/Apple-Pie-AI/pie-tui/main/install.sh | bash
```

Then `pie init` to configure your repo.

| Prerequisite | Why | Check with |
|---|---|---|
| [Claude Code](https://claude.ai/download), authenticated | Every stage runs through `claude -p` | `claude --version` |
| `git` | Worktrees, branches, commits | `git --version` |
| [`gh`](https://cli.github.com), authenticated | Opens and tracks pull requests | `gh auth status` |
| [Android Studio](https://developer.android.com/studio) | Only for tickets that need an emulator | `pie doctor` |
| [Jira](docs/jira.md) | Optional — local `.md` tickets work without it | `pie doctor` |

<details>
<summary>Manual install</summary>

Download the binary for your platform:

```bash
# macOS (Apple Silicon)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_darwin_arm64
# macOS (Intel)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_darwin_amd64
# Linux (x86-64)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_linux_amd64
# Linux (arm64)
curl -fsSL -o pie https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_linux_arm64
```

Then install it to your user bin (creates the folder, no `sudo`):

```bash
mkdir -p ~/.local/bin && mv pie ~/.local/bin/
```

If `~/.local/bin` isn't on your `PATH` yet:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
```

On macOS, if the first run is blocked with `zsh: killed` or a Gatekeeper warning,
clear the download quarantine flag:

```bash
xattr -d com.apple.quarantine ~/.local/bin/pie 2>/dev/null
```

</details>

Versioned archives (`.zip` for macOS, `.tar.gz` for Linux) and a SHA-256 `checksums.txt` are attached to every release.

Prefer to build it yourself? See [Build from source](#build-from-source).
## First steps

Never run this before? Do these eight steps in order. Nothing here touches your repo or opens a PR until step 8.

**1. Check your prerequisites.**

```bash
claude --version && gh auth status && git --version
```

Claude Code has to have been launched and signed in at least once. `gh` should say *Logged in*. Android Studio only matters for tickets that need an emulator, so skip it for now.

**2. Install, then confirm the binary is on your PATH.**

```bash
curl -fsSL https://raw.githubusercontent.com/Apple-Pie-AI/pie-tui/main/install.sh | bash
pie --help
```

**Check:** `pie --help` prints the command list. If you get `command not found`, `~/.local/bin` isn't on your PATH — the installer prints the exact line to add for your shell.

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

**Check:** the three required checks — git origin, `claude`, `gh` — pass. The optional ones (`adb`, `emulator`, AVD name) can fail for now; they only matter once a ticket needs instrumented tests.

**5. Write your first ticket as a markdown file.**

```bash
cat > ~/first-ticket.md <<'EOF'
# Add a TODO comment to the main Activity

Add a single `// TODO: hello from Apple Pie` comment at the top of the
app's main Activity class. Nothing else.
EOF
```

The ticket id comes from the **filename**: uppercased, with every run of non-alphanumeric characters collapsed to a dash. So `first-ticket.md` becomes `FIRST-TICKET`, and that id names the worktree, the log file, and the branch.

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

**8. Do it for real.** Drop `--dry-run` on a second local ticket to get an actual pull request, or connect [Jira](docs/jira.md) and run `pie run PROJ-123`.

Whichever you pick, the guarantee is the same: **it stops at `review`. Apple Pie never merges anything.** When you're done experimenting, choose **Stop & clean up** from a ticket's menu in the TUI to stop it and remove its worktree.

## Using it day to day

Bare `pie` is the dashboard, and it's where most of the work happens: choose **Start new ticket(s)**, write or paste a ticket, confirm its id and branch name, press enter. Start several and they run in parallel. <kbd>:</kbd> opens the command palette for doctor, config, and the daemon.

The CLI does the same things:

```bash
pie run PROJ-123              # one ticket → PR
pie run a.md b.md c.md        # local markdown tickets, in parallel
pie status                    # table of all sessions (--watch to follow)
pie logs PROJ-123             # print the session log for a ticket
pie doctor                    # check git, claude, and gh connectivity
```

Every command and `pie run` flag is in [docs/cli.md](docs/cli.md).

## Configuration

Everything Apple Pie owns lives in `~/.pie`: `config.toml` (mode 0600), the SQLite state, one worktree per ticket, and archived plans, reports, and logs. Secrets never go in the config file — they live in your OS keychain, or in `PIE_*` environment variables for headless use.

The settings you'll touch most are the gates (`review_plans`, `review_before_pr`), the per-stage models (easiest from **Edit models**), and `max_budget_usd`. Everything else is in [docs/configuration.md](docs/configuration.md); the command allowlist and approvals are in [docs/permissions.md](docs/permissions.md).

## Cost, telemetry, and privacy

Apple Pie runs on your existing Claude Code subscription or API key. There's no separate account and no markup. Each ticket costs a full working session's worth of tokens, so five parallel tickets is roughly five concurrent sessions. Start with one.

Telemetry is opt-in, asked once during `pie init`. When enabled it sends three events — `run_started`, `run_completed`, `tui_opened` — carrying a randomly generated device id, your OS and architecture, the Apple Pie version, and for a completed run its outcome state, duration in seconds, and whether it errored. **No ticket content, no file paths, no repo or branch names, no code.** Turn it off any time with `telemetry_enabled = false` in `~/.pie/config.toml`.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `pie: command not found` | `~/.local/bin` isn't on your PATH. The installer prints the line to add |
| A run fails immediately | `pie doctor` — it's almost always `gh` or `claude` auth |
| A ticket is stuck or vanished | `pie logs <TICKET>` has the full session. `pie status` shows every state |
| A ticket landed in NEEDS YOU | Read the reason in the TUI, fix it in `~/.pie/worktrees/<TICKET>`, then `pie run <TICKET> --resume` |
| Verification skips instrumented tests | `pie doctor` — `avd_name` is probably unset, so it fell back to unit tests |
| A worktree is wedged | Choose **Stop & clean up** from its menu in the TUI, then re-run |
| `go.mod requires go >= 1.26.2` | Building from source with an older Go: prefix with `GOTOOLCHAIN=auto` |

## Build from source

Apple Pie is a single Go binary with no cgo. It shells out to the real tools rather than wrapping their SDKs: your `git`, your `gh`, your `claude`, your Android SDK — same binaries, same auth, same config you already use.

```bash
git clone https://github.com/Apple-Pie-AI/pie-tui && cd pie-tui
make build      # → ./pie
make install    # → ~/.local/bin/pie
make test       # go test ./...
```

Go 1.26.2 or newer. On an older toolchain, prefix with `GOTOOLCHAIN=auto` and Go will fetch the right one.

## Documentation

- [docs/workflows.md](docs/workflows.md) — each workflow step by step, every gate, the lifecycle states, and review-comment handling in depth
- [docs/cli.md](docs/cli.md) — every command and `pie run` flag
- [docs/configuration.md](docs/configuration.md) — the full config reference
- [docs/permissions.md](docs/permissions.md) — the command allowlist, rule syntax, and approvals
- [docs/jira.md](docs/jira.md) — connecting Jira
- [docs/architecture.md](docs/architecture.md) — packages, the agent contract, worktrees, the emulator semaphore
- [RELEASE.md](RELEASE.md) — building release binaries and cutting a release

## Uninstall

```bash
pie stop 2>/dev/null                              # stop the daemon if it's running
rm ~/.local/bin/pie                               # remove the binary
rm -rf ~/.pie                                     # config, state, logs, worktrees
security delete-generic-password -s pie 2>/dev/null   # macOS: any stored keys
```

Everything Apple Pie writes lives under `~/.pie`, so that's the whole of it.

## License

MIT — see [LICENSE](LICENSE).
