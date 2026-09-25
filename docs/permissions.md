# Permissions & approvals

How Apple Pie's agents are permission-gated, what happens when a command is
refused, and how to read it all in the logs. Written after a field incident
(PLEX-61263) where an `ask` rule in the user's own Claude settings dead-ended a
ticket with a message blaming the wrong config — everything here exists so
that class of failure is either prevented or diagnosable in one glance.

## The three permission layers

Agent permissions stack additively. Apple Pie can widen the gate, never narrow it:

1. **The user's/org's own Claude Code settings** — managed org file
   (`/Library/Application Support/ClaudeCode/managed-settings.json` on macOS),
   `~/.claude/settings.json`, the repo's tracked `.claude/settings.json`, and
   the worktree's seeded `.claude/settings.local.json`. These always apply.
   `deny` rules here are absolute; `ask` rules demand a human decision.
2. **Apple Pie's allowlist** — the built-in default (or `allowed_tools` when a
   user replaces it), passed as `--allowed-tools` to the implement, verify and
   comment-fix stages.
3. **`extra_allowed_tools`** — append-only additions on top.

Empirically proven (claude 2.1.232, live repros): an `ask` rule **beats**
`--allowed-tools`, a same-layer `allow`, `--permission-mode bypassPermissions`,
and `--dangerously-skip-permissions`. **No flag escapes an ask rule** — it is
not trying to be beaten, it is trying to reach a human. The only escape is to
supply the human: `--permission-prompt-tool`.

## Rule syntax (how to read an allowlist)

The syntax is Claude Code's own — the same strings work in
`~/.claude/settings.json`'s `permissions.allow/ask/deny`. Two kinds of token:

**Bare names grant one of Claude Code's built-in tools** (structured calls,
no shell involved): `Read` reads a file by path, `Glob` finds files by
pattern, `Grep` searches file contents, `Edit`/`MultiEdit` make targeted
string replacements, `Write` creates or overwrites a file, `TodoWrite` is
the agent's internal checklist (touches nothing). `Read` does **not** cover
`ls` or `cat` — those are shell commands, gated separately.

**`Bash(...)` rules gate the shell tool per command.** `Bash(ls:*)` allows
any command whose first token is `ls`; `Bash(git diff:*)` anything starting
with `git diff`; a rule without `:*` — `Bash(cd App && ./gradlew test)`,
the shape *Allow & remember exact* writes — matches only that verbatim
string. Compound commands are checked **per segment**: `cd App && ./gradlew
test` needs both `Bash(cd:*)` and `Bash(./gradlew:*)` (or one exact rule
for the whole compound).

That's why the default list carries both `Read`/`Glob`/`Grep` *and*
`Bash(cat:*)`/`Bash(ls:*)`/`Bash(grep:*)`: an agent may read a file through
the structured tool or through the shell, and each route is permissioned
independently.

## The approval callback

Apple Pie supplies that human. Allowlist-mode stages pass
`--permission-prompt-tool mcp__pie__approve` with an mcp-config that makes
claude spawn `pie mcp-approve --ticket <T>` (the same binary) as a stdio MCP
server. When the agent hits an **ask rule** or an **allowlist gap**, claude
asks that server instead of hard-denying:

```
pie run ──spawns──▶ claude -p ──spawns──▶ pie mcp-approve
   │                    │                  (stdio JSON-RPC)
   │                    │ ask rule / allowlist gap →
   │                    │ ── "may I run X?" ──▶ │
   │                    │                       │ 1) X matches pie's allowlist?
   │                    │                       │    → ALLOW instantly (zero-touch)
   │                    │                       │ 2) else: pending row in the store ─┐
   │                    │                       │                                    ▼
   │                    │                       │            dashboard: "agent wants to run: X"
   │                    │                       │            Allow once / Allow & remember / Deny
   │                    │                       │ ◀── decision ──┘
   │                    │ ◀── allow/deny ───────┘   (no timeout: the question
   │                    │ command runs in-session    waits until you answer)
```

Rules of the flow:

- **Auto-approve** (`approval_policy = "auto"`, the default): a command that
  already matches Apple Pie's effective allowlist is approved with no human —
  the human pre-approved that shape by allowlisting it. `"always-ask"` routes
  every callback to the dashboard instead.
- **The dashboard prompt**: banner (`⏸ N command(s) waiting … press A`), the
  ticket row's phase flips to `approve? ⏸`, and Enter on the row opens the
  prompt directly. The command is shown verbatim, then up to four rows — only
  the ones that apply to this command:

  ```
    Bash: ./gradlew :shared:allTests

    Allow once
    Allow & remember exact → Bash(./gradlew :shared:allTests)
    Allow & remember all    → Bash(./gradlew:*)
    Deny
  ```

  *Allow once* answers just this call, no config write. The two *remember*
  rows each show the verbatim rule they would write **before** you choose —
  *exact* covers only this precise command, *all* generalizes to every
  invocation of the binary; whichever you pick is written exactly as shown,
  never re-derived. Rows that would be unsafe to offer (a guarded command
  like `git checkout`, or a prose-shaped denial) are left off entirely — you
  never see a remember option that silently degrades to allow-once. *Deny*
  refuses it; Esc leaves it pending. The agent continues **in the same
  session** the moment you decide.
- **No timeout — ever.** A pending question waits until you answer, however
  long that takes: pie never decides a permission on your behalf, because the
  human driving every grant and refusal is the point — you stay part of the
  process, and responsible for what the tool is allowed to do. The only
  structural exits are the claude session that asked dying (its prompt is
  expired so the dashboard never shows a question no agent is behind), and
  you stopping or restarting the run (same cleanup).

  One honest footnote: claude's own MCP layer aborts a tool call that stays
  silent (~30 minutes by default — field QA hit "sent no response or progress
  for 1938s; aborting" on a prompt left pending overnight). Pie raises the
  per-server `timeout` in the mcp-config it writes to **7 days**, so a single
  question effectively waits as long as any real workflow needs; the knob and
  its millisecond unit are pinned by the `-tags repro` suite so a CLI change
  gets caught before an overnight prompt does.
- **`deny` rules never reach the callback.** An explicit deny in the user's
  Claude settings stays a hard block — deliberately. Apple Pie will not work
  around it.
- Auto and bypass permission modes are untouched; the callback rides only
  with the allowlist mode.

## Denial flavors

When a command IS refused, the raw error text tells you why — each cause has a
distinct fingerprint (observed on claude 2.1.232; `go test -tags repro
./internal/agent` re-validates them against the installed CLI):

| Flavor | Raw CLI error contains | Meaning / remedy |
|---|---|---|
| `allowlist-gap` | "requires approval" | A segment matched no allow rule. Fixable in pie's config — the one-key *Allow denied commands & re-run* |
| `ask-rule` | "haven't granted it yet" | An ask rule in the user's Claude settings wanted a human. The callback handles it live; a park names the rule and file |
| `deny-rule` | "has been denied" | An explicit deny in the user's Claude settings. Apple Pie will not work around it |
| `cd-containment` | "cd in '…' was blocked" | claude's own working-directory guard |
| `human-denied` | "denied from the pie dashboard" | you pressed Deny on the approval prompt — the park says so, and offers Resume-and-approve or the one-key allow as the "if it was a mistake" path |
| `unknown` | anything else | The park quotes the CLI verbatim |

Park messages are flavor-diagnosed: an ask-rule park says *"rule
`Bash(./gradlew:*)` in ~/.claude/settings.json"*, not "check the logs".

## The allowlist suggestion ladder

*Allow denied commands & re-run* (and *Allow & remember*) derive rules in two
escalating steps, then stop:

1. **Generalize**: `cd App && ./gradlew test` → `Bash(cd:*)`, `Bash(./gradlew:*)`.
2. **Exact rule**: when every head is already allowed yet the CLI refused
   anyway, suggest the verbatim command — `Bash(cd App && ./gradlew test)`.
   Exact rules are whitespace-brittle by design; the callback is the backstop.
3. **Silence**: both present → nothing to suggest; the flavor message and the
   callback own whatever remains.

Guarded commands (git state mutations, `rm`, `sudo`), prose, and one-off
absolute-path scripts are never suggested at any rung.

## Reviewing and editing what's been granted

*Allow & remember* accumulates rules into `extra_allowed_tools` over time —
review or undo any of it from **Edit config** (the pinned dashboard row, or
the `:` command palette). That screen leads with **Edit command allowlist**,
a link into a dedicated allowlist screen, followed directly by the usual
repo path, branch pattern, base branch, three model overrides, and
plan-review toggle — one hop to touch a field, one more hop (from the link)
to review the allowlist, so a config with a long custom `allowed_tools`
baseline doesn't bury the fields under a dump of rules.

The allowlist screen shows:

- **Baseline** (read-only) — the active `allowed_tools` (default, or your
  custom replacement) with its rule count, so you can see what's already
  covered before adding to it. Not editable here: it's the "expert knob" for
  wholesale replacement, and editing it inline risks silently losing default
  upgrades — edit `allowed_tools` directly in `~/.pie/config.toml` instead if
  you really mean to replace it.
- **Extra rules** (editable) — one row per rule in `extra_allowed_tools`.
  `Enter` on a rule opens a small confirmation dialog showing that exact
  rule — **Remove it** deletes it from the config right there (agents lose
  the grant immediately); **Keep it** or `Esc` closes the dialog with nothing
  changed. The dialog is the safety net a separate save button used to be:
  there is no staged state to lose and no Save row to find. **+ Add rule**
  opens a single-line input for typing a new rule by hand, validated and
  written immediately on `Enter` (an invalid string, or one already in the
  list, shows an inline error and stays open for correction).

When the list outgrows the terminal, the screen scrolls to keep the focused
row visible, with `… N more above` / `… N more below` markers standing in
for what's cut off.

Every screen in the hub, including this one, is operable with only `↑↓` /
`Enter` / `Esc` — no single-letter shortcuts to hunt a footer hint for (see
the TUI convention in `CLAUDE.md`).

Field edits and allowlist edits stay independent — editing a config field
never touches `extra_allowed_tools`, and editing the allowlist never touches
the fields.

**A note on scope, honestly**: restricting a rule to one Gradle module or
task (`Bash(./gradlew :shared:test:*)` vs `Bash(./gradlew:*)`) is not a real
security boundary — any Gradle task executes the project's build scripts,
which are arbitrary code, so a narrower task name doesn't narrow what the
agent could actually do. The allowlist's job is preventing *accidents*, not
containing a malicious build file; the *exact* remember scope exists for
that (fewer surprising invocations matching), and *deny* rules plus the
sandbox (`docs/configuration.md`) are the actual boundaries.

## Reading the logs

Every run's log (`pie logs <ticket>`, the TUI detail pane, or
`~/.pie/logs/<ticket>.log`) opens with two diagnostics lines — check them
first, they answer "what actually ran":

```
[T] claude CLI: 2.1.232 (Claude Code)      ← which CLI version answered
[T] allowlist: 30 rule(s): Edit Write …    ← the allowlist actually in effect
```

The log file accumulates across runs of the same ticket; jump to the **last**
`claude CLI:` line and read down.

Callback decisions are written by `pie mcp-approve` with a `⏸` prefix
(permission-prompt calls never appear in the session stream — these lines are
the only trace):

| Line | Meaning |
|---|---|
| `⏸ approve: auto-approved by pie allowlist: Bash(…)` | Ask rule / gap intercepted the command; pie answered allow with no human |
| `⏸ approve: awaiting human decision in the pie dashboard: Bash(…)` | A prompt is (or was) waiting in the TUI |
| `⏸ approve: allowed from the dashboard: Bash(…)` | You pressed Allow |
| `✗ tool error: … denied from the pie dashboard` | You pressed Deny |

Refusals and parks:

| Line | Meaning |
|---|---|
| `✗ permission denied: Bash(…)` | A command was hard-refused (per denial, verbatim) |
| `needs-you: verify blocked by permissions (N denial(s), <flavor>)` | The park, with its diagnosed flavor |
| `verify: agent certified build + tests green ✓` | The verify gate passed |

Quick verdicts:

```bash
L=~/.pie/logs/TICKET.log
grep -c "auto-approved" $L        # zero-touch approvals this ticket
grep -c "permission denied" $L    # hard refusals (want 0 on a clean run)
grep -E "awaiting human|allowed from the dashboard" $L   # prompts you answered
```

## Config keys

```toml
approval_policy = ""    # "" / "auto": auto-approve allowlisted commands; "always-ask": every callback prompts
```

There is deliberately no timeout knob: a pending prompt waits for the human.

Plus the allowlist keys documented in [configuration.md](configuration.md):
`allowed_tools` (expert replacement of the baseline) and
`extra_allowed_tools` (append-only, what *Allow & remember* writes to).
