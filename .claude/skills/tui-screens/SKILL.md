---
name: tui-screens
description: Reference glossary mapping Apple Pie TUI screen/flow names (dashboard, plan review, change/commit review, PR comments triage, fix-review, settings, etc.) to their exact hubView constant and source files in internal/tui. Use when resolving what "the summary screen" or "commit review" means, or which file owns a given TUI screen.
---

# tui-screens

Reference glossary for Apple Pie's TUI (`internal/tui`). Use this to resolve a
screen/flow name to its exact `hubView` constant and source files instead of
guessing — "the summary screen", "settings", "commit review" all mean one
specific thing below.

Every screen is ↑↓ / Enter / Esc only (see CLAUDE.md's TUI convention). The
dashboard is `hubView` zero value; every other screen is one `viewX` constant
in `internal/tui/tui.go`, routed by `view.go` (render) and `update.go` (keys).

## Screens

| Name | `hubView` const | Key files | What it is |
|---|---|---|---|
| **Init wizard** | *(not a TUI view — `pie init`)* | `cmd/init.go` | Interactive first-run setup, runs before the hub ever opens. Scaffolds `~/.pie/config.toml` (repo path, branch pattern). Non-interactive stdin gets a commented sample config instead. |
| **Consent screen** | `viewConsent` | `settings.go` | First-run-only telemetry opt-in, shown before the dashboard if consent hasn't been given yet. |
| **Dashboard** | `viewDashboard` (zero value) | `dashboard.go`, `dashboard_layout.go`, `dashboard_row.go`, `dashboard_rows.go`, `dashboard_why.go` | The hub's home screen. Sessions triaged into groups (NEEDS YOU / RUNNING / READY FOR REVIEW / TRACKING / CLOSED, see `store/states.go`), plus the 4 top command rows: Start new ticket(s), Checkout a branch, Edit config, Edit models. |
| **Command palette** | `viewPalette` | `palette.go` | `:`-opened list of commands, scoped to the selected agent row or global. |
| **Start new ticket(s)** | `viewRunMethod` → `viewRunInput` | `run.go`, `run_start.go`, `run_content.go`, `run_pickers.go`, `run_prompts.go` | The "queue tickets" wizard: pick content vs. Jira, paste/describe, per-chip id/branch/image/stack-base/plan-review prompts, then launch. |
| **Plan review screen** | `viewPlan` | `plan.go` | Full-screen rendered plan, gate after the plan stage (ticket state `plan-review`, opt-in). Approve or type feedback to re-plan. |
| **Change screen** *(= commit review / pre-PR review)* | `viewChange` | `change.go`, `change_data.go`, `change_box.go`, `change_view.go` | Review a verified local change before its PR exists (ticket state `change-review`). Split view: file list + amber "Ship it" CTA on the left, focused file's diff on the right, feedback box underneath. First row of the list is **Summary** — the pane showing the draft PR description text (`changeSummaryLines`). Two verbs only: fix it (feedback → rework) or ship it (approve → verify-if-moved, commit, push, PR). |
| **PR review comments screen** | `viewComments` (triage mode) | `comments.go`, `comments_row.go`, `comments_menu.go`, `comments_layout.go`, `comments_nav.go`, `comments_fetch.go` | Master-detail triage of open GitHub PR review threads (ticket state `review` with open comments). Every comment starts selected; the work is deselecting exceptions. Primary action is the run bar that sends selected comments to the agent. |
| **Fix-review approve screen** *(same `viewComments`, approve phase)* | `viewComments` (card mode) | `comments_approve.go`, `comments_approve_view.go`, `comments_cards.go`, `comments_cards_view.go`, `comments_resolve.go` | The agent has fixed queued comments locally but nothing is committed/pushed/posted yet (ticket state `fix-review`). Per-comment cards show the draft reply + diff; verbs are edit reply, send back with feedback, or approve (ship) all. |
| **Edit config screen** *(= "Settings")* | `viewEditConfig` | `editconfig.go` | Reached from the dashboard's "Edit config" row. The command Allowlist link leads, followed by the general fields (repo path, branch pattern, plan-review default), then Save. |
| **Edit models screen** | `viewEditModels` | `editmodels.go`, `editmodels_options.go`, `editmodels_view.go` | Reached from the dashboard's "Edit models" row (and the `:` palette). Per-stage model overrides (plan/impl/verify/review/comment-fix), each picked from a list: the stage default, the company's Claude Code `/model` list (`modelPicker`, via `agent.ClaudeModelPicker`), the `agent.ModelAliases`, the user's `saved_models`, then Add/Remove a saved model. A picked model gets a one-turn background check (`agent.CheckModel`) shown on its row. Files: `editmodels.go` (state/keys), `editmodels_options.go` (options, checks), `editmodels_view.go`. |
| **Permissions / allowlist screen** | `viewPermissions` | `permissions.go` | Linked from Edit config's first row. Shows the baseline allowlist (read-only) plus `extra_allowed_tools` rows, add/remove — edits apply immediately, no separate Save. |
| **Repo-fix screen** | `viewRepoFix` | `repofix.go` | Bounced to from a failed run preflight when the configured repo path is invalid: pick a nearby local checkout, clone from a URL, or drop into Edit config to fix the path by hand. |
| **Doctor / output screen** | `viewOutput` | `settings.go` | Read-only scrollback for `pie doctor` output and similar one-shot command results. |
| **Approval overlay** | *(overlay, not a `hubView`)* | `approvals.go` | Pre-empts whatever screen is active when an agent is paused on a command its allowlist doesn't cover. Allow once / allow & remember / deny. |
| **Answer overlay** | *(overlay via `m.answer.open`)* | `tui.go` (`answerState`), routed in `update.go` | Pre-empts the active screen for `needs-you`/`awaiting-answer` sessions: type an answer, agent resumes. |
| **Stop/cleanup confirm dialog** | *(overlay via `m.confirm`)* | `view.go` (`renderConfirm`) | "Yes, stop" / "No, keep running" dialog before killing a running session. |

## Ticket lifecycle → screen you land on

Pressing Enter on a dashboard row opens the screen matching that ticket's
`store` state (`internal/store/states.go`):

- `plan-review` → **Plan review screen**
- `change-review` → **Change screen**
- `fix-review` → **Fix-review approve screen**
- `review` with open comments → **PR review comments screen** (triage mode)
- `needs-you` / `awaiting-answer` / `failed` → **Answer overlay**
- everything else idle/terminal (`merged`, `closed`, `stopped`, `checked-out`) → dashboard row only, no screen

## Notes

- `m.ownsHeader()` in `view.go` lists which screens draw their own header
  (Dashboard, Palette, Comments, Change) vs. taking the shared frame header —
  useful when a screen's rendering looks "different" from the rest.
- The Change screen's **Summary** pane and the Fix-review approve screen's
  **draft replies** are the two places PR/comment text is edited before it
  ships — don't conflate them when a bug report says "the summary is wrong."
