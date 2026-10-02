# Apple Pie - repo conventions

Greenfield Go project. **Apple Pie** - a native mobile app harness that drives
agentic CLIs (Claude Code, Codex) to run enterprise mobile development cycles. See
`.context/attachments/jIQXSc/plan-handoff.md` for the binding spec and decisions.

**Don't overclaim.** What ships today is Claude Code only (`internal/agent` shells out
to `claude -p`) and Android only (`internal/emulator` is the AVD manager, and the
config defaults bake in Gradle). Codex, other agentic CLIs, and non-Android platforms are
positioning, not code - keep docs and comments honest about that line.

## Layout
- `main.go` - entrypoint, at the repo root (`.goreleaser.yaml` builds `main: .`).
- `cmd/` - Cobra wiring only, one file per command: `root`, `run`, `start`/`stop`,
  `status`, `doctor`, `init`, `monitor`, `stubs` (=`logs`), `splash`. `var version`
  lives in `root.go` and is the `-ldflags -X` target named by both the Makefile and
  `.goreleaser.yaml` - it does not move.
- `internal/tui` - the whole Bubble Tea hub, one file per screen. `tui.Run(version)` is
  its entrypoint (plus `RunDemo` below); `tui.go` holds the model and view enum, `update.go` is the single
  key router, `view.go` the render router, `styles.go` every color and style.
  `demo.go` is the hidden `pie --demo` (`tui.RunDemo`): the real hub on fixture tickets
  in a throwaway `PIE_HOME`, on a scripted timeline that `demo/apple-pie.tape` (VHS,
  `make demo`) is timed against - retime one, re-check the other.
- `internal/ticket` - local `.md` ticket parsing, id derivation, and the
  `~/.pie/pasted/<id>/` read/write pair. Shared by `pie run` and the TUI.
- `internal/runner` - the per-ticket pipeline, split by stage: `runner.go` (the staged
  `Run`), `task.go` (`Task`/`Hooks`/`Outcome` and the two ways a run ends early),
  `ship.go` (commit/push/PR + the resume and ship paths), `verify.go`,
  `emulator.go`, `archive.go`, `naming.go`. `Run` takes a `context.Context`;
  cancelling it stops the agent and unwinds at the next stage boundary.
- `internal/agent` - drives `claude -p` (Decisions 3, 6): `agent.go` (subprocess +
  stream-json), `contract.go` (the `.agent/` JSON contract and its readers),
  `prompts.go` (the prompt corpus).
- `internal/store` - SQLite session state (Decision 2): `store.go` (schema/open),
  `sessions.go`, `emulator.go` (the semaphore), `states.go` (the lifecycle vocabulary).
- `internal/daemon` - the poll loop behind `pie start`/`stop`.
- `internal/config` - `~/.pie/config.toml` loader (Decision 15).
- `internal/secrets` - OS keychain + env fallback (Decision 14).
- `internal/proc` - process liveness (`Alive`) and termination (`TerminateGroup`,
  which waits for the process to actually be gone), plus the daemon pidfile.
- `internal/jira` - Jira Cloud read + comment.
- `internal/git` - worktree-per-ticket + branch mgmt (Decision 7).
- `internal/emulator` - Android emulator/AVD manager (boot, wait, kill) for the verify
  stage. It runs no build; the agent does that and self-certifies.
- `internal/vcs` - `gh` PR + templated PR body / Jira comment (Decision 10).
- `internal/telemetry` - fire-and-forget PostHog events. `APIKey` is an ldflags var;
  the version is passed in by the caller, never duplicated as a package global.
- `internal/paths` - `~/.pie` layout, and the shared worktree-readiness check.
- `internal/splash` - first-run "Apple Pie" title screen (`--splash` replays it).
- `internal/sound` - chiptune cues for the splash: PCM synthesized in pure Go, played via the OS's own audio command. **No audio library** - the release is `CGO_ENABLED=0` cross-compiled, and every Go audio lib needs cgo.

## Build & run
```
go build -o pie .
./pie init          # scaffold ~/.pie/config.toml
./pie run PROJ-123  # one ticket → one PR
```

`go.mod` requires Go 1.26.2. If your local toolchain is older, prefix commands with
`GOTOOLCHAIN=auto` (`GOTOOLCHAIN=auto go test ./...`) or they fail before compiling.

## Phasing

Phases 1-3 have landed - treat all of the below as built, not as work to do:
- **Phase 1:** `pie run <TICKET>` - one ticket end to end, no daemon.
- **Phase 2:** SQLite state (`internal/store`), daemon poll loop (`internal/daemon`,
  behind `pie start`/`stop`), concurrency, `pie status` (Decisions 2, 8, 9).
- **Phase 3:** interactive `pie init` wizard, GoReleaser + Homebrew (Decisions 13, 17).

Genuinely unbuilt (positioning, not code - see the "Don't overclaim" note above):
a Codex / non-Claude agent adapter, and any non-Android platform (iOS, simulators).

## Conventions
- Keep packages small and dependency-light; shell out to `git`/`gh`/`claude` rather than wrapping SDKs.
- The agent edits code **and verifies it** - it discovers + runs the project's own build/tests (handling per-project setups and company build skills) and self-certifies via `report.json` `verified`. The **orchestrator** owns commit, push, and PR, and trusts that verdict (it no longer runs Gradle itself).
- Never auto-merge - stop at `review`.
- One concern per file, and no file should reach ~400 lines. When one does, split it
  along the seams it already has rather than growing another section banner.
- I/O the pipeline shouldn't own is **inverted at the caller**: Jira posts through
  `runner.Hooks.Comment`, the Anthropic key arrives on `runner.Task`, telemetry consent
  never reaches the runner at all. Keep it that way - it is what lets local `.md`
  tickets run with no Jira config and no branching inside the pipeline.
- **TUI menu/list interaction is ↑↓ / Enter / Esc, and nothing else.** Every
  selectable row in the hub (dashboard rows, palettes, the approval overlay,
  the config/allowlist screens) must be operable with only: `↑↓` to move the
  cursor, `Enter` to act on the focused row (its meaning is row-dependent -
  toggle, open, apply - and that is fine), and `Esc` to cancel/close. Do not
  bind single-letter shortcuts (`d`, `x`, `a`, ...) for row actions like
  delete or toggle - they require hunting a footer hint to discover and are
  inconsistent with every other screen in the hub. The one exception is a
  free-text input a row opens (the allowlist's add-rule field, the ticket
  content editor): typing is the point there, and `Enter`/`Esc` still
  bookend the field as usual.

## Decisions we do not re-litigate
1. **`main.go` stays at the repo root and `cmd/` keeps its name.** Both the Makefile and
   `.goreleaser.yaml` reference `-X .../cmd.version`, and goreleaser builds `main: .`.
   Renaming `cmd/` to `internal/cli` or moving to `cmd/pie/main.go` buys nothing for a
   binary nobody imports, and breaks version injection - which no test covers.
2. **No `/pkg` directory, no `golang-standards/project-layout`.** Everything that isn't
   the entrypoint or Cobra wiring is `internal/`.
3. **No interface-per-struct and no DI framework.** Consumers accept concrete types
   until a second implementation actually exists.
4. **Prompts stay Go string constants in `agent/prompts.go`**, not `//go:embed` +
   `text/template` - `go vet`'s printf checker covers them where they are.
18. **Released binaries are obfuscated with garble at max settings** (`-literals -tiny
   -seed=random`, on top of `-s -w`). GoReleaser calls `scripts/garble.sh` as its
   `gobinary`; that wrapper honors `PIE_NO_OBFUSCATE=1` for fast plain builds. This is
   safe here because all (de)serialization uses explicit `json:`/`toml:` struct tags and
   production code never reflects on field names, so identifier renaming can't break it.
   `-tiny` intentionally strips file:line info (crash telemetry loses stack detail - the
   point is "no easy traces"). **garble needs a real GOROOT** - the `GOTOOLCHAIN` toolchain-
   download mechanism fails (`overlay ... must not be replaced` under GOMODCACHE), so install
   a proper SDK via `golang.org/dl` - currently `go1.27.1`, because garble v0.18.0 requires Go 1.27+
   even though `go.mod` only asks for 1.26.2 (see RELEASE.md). No UPX/packing (breaks macOS codesign).

## Skill routing

When the user's request matches an available skill, invoke it via the Skill tool. When in doubt, invoke the skill.

Key routing rules:
- Product ideas/brainstorming → invoke /office-hours
- Strategy/scope → invoke /plan-ceo-review
- Architecture → invoke /plan-eng-review
- Design system/plan review → invoke /design-consultation or /plan-design-review
- Full review pipeline → invoke /autoplan
- Bugs/errors → invoke /investigate
- QA/testing site behavior → invoke /qa or /qa-only
- Code review/diff check → invoke /review
- Visual polish → invoke /design-review
- Ship/deploy/PR → invoke /ship or /land-and-deploy
- Save progress → invoke /context-save
- Resume context → invoke /context-restore
- Author a backlog-ready spec/issue → invoke /spec
