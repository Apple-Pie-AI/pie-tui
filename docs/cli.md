# CLI reference

Bare `pie` opens the dashboard, and most work happens there. Everything is also available from the command line.

## Commands

```bash
pie run PROJ-123              # one ticket → PR
pie run PROJ-123 PROJ-124     # two tickets in parallel
pie run a.md b.md c.md        # local markdown tickets, in parallel

pie doctor                    # check git, claude, and gh connectivity
pie logs PROJ-123             # print the session log for a ticket
pie status                    # table of all sessions (--watch to follow)
pie start                     # background cleanup daemon (--once for a single pass)
pie stop                      # stop the daemon
pie --splash                  # replay the title screen
```

`pie start` is a janitor, not a scheduler — it reclaims worktrees once their PRs are merged or closed, and shuts the emulator down when it's been idle. Tickets are always started by you, from `pie run` or the TUI.

## Every `pie run` flag

| Flag | What it does |
|---|---|
| `--dry-run` | Everything except `git push` and the PR. Try this first |
| `--review-plan` | Pause after planning so you can approve or redirect before any code is written |
| `--branch <name>` | Exact branch name for the PR, overriding the config pattern. One ticket at a time |
| `--from-branch <name>` | Work ON an existing branch instead of creating one — address PR feedback, or have the agent review a PR's code. Never resets the branch. One ticket at a time |
| `--base <ref>` | Base branch — or another ticket's id — to stack on. The PR targets it |
| `--repo <path>` | Which configured repo to use. Defaults to the first one |
| `--resume` | Keep your manual fixes in the existing worktree, re-run verification, then PR |
| `--ship` | Your explicit override: skip verification and commit, push, and PR your own manual fix. See below |
| `--from-plan` | Skip planning and implement from the `plan.json` already in the worktree |
| `--address-comments` | Fix the PR review comments selected in the dashboard — locally only, then pause at `fixes ready` for your approval. The TUI spawns this for you |
| `--feedback <text>` | With `--address-comments`: revise the local fixes per this correction, resuming the fix session. The TUI's "Request changes" sends this |
| `--ship-comments` | The approval: verify, commit, push to the PR, and post the previewed replies. The TUI's approve bar spawns this |
| `--local` | Skip Jira and take the ticket from `--title`/`--desc` |
| `--title` / `--desc` | The ticket summary and body, with `--local` |
| `--splash` | Replay the title screen first. Works on any command |

`--base` accepts a ticket id as well as a branch name, so `pie run PROJ-124 --base PROJ-123` builds on the branch PROJ-123 created and opens its PR against it. A plain re-run of a stacked ticket keeps its base rather than silently resetting to the default branch.

**About `--ship`.** By default Apple Pie will not open a PR without a fresh, successful verification report. `--ship` is a human override for when you've fixed and checked the change yourself, or deliberately want to bypass that check. Only you can trigger it: from the CLI, or with **Open a PR** in a stuck ticket's menu, which says it skips verification.
