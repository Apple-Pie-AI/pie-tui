# 🥧 Apple Pie

**Apple Pie (`pie`)** is a terminal harness that drives agentic CLIs (Claude Code) through
full mobile development cycles: it takes a ticket, spins up an isolated git worktree,
lets the agent implement and verify the change on an Android emulator, and stops at a
pull request for human review. One ticket in, one PR out — never auto-merged.

This repository hosts the **binary releases**. Grab the latest from the
[Releases page](https://github.com/Apple-Pie-AI/pie-tui/releases/latest).

## Install

### macOS (Apple Silicon)

```sh
curl -fsSL -o pie.zip https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_2.1.0_darwin_arm64.zip
unzip pie.zip pie && chmod +x pie && sudo mv pie /usr/local/bin/
pie --version
```

### macOS (Intel)

```sh
curl -fsSL -o pie.zip https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_2.1.0_darwin_amd64.zip
unzip pie.zip pie && chmod +x pie && sudo mv pie /usr/local/bin/
pie --version
```

> **Gatekeeper note:** the binaries are ad-hoc signed. If you download through a
> browser instead of `curl`, macOS may quarantine the file — clear it with
> `xattr -d com.apple.quarantine /usr/local/bin/pie`.

### Linux

```sh
# amd64 (swap in arm64 if that's your machine)
curl -fsSL https://github.com/Apple-Pie-AI/pie-tui/releases/latest/download/pie_2.1.0_linux_amd64.tar.gz | tar xz pie
chmod +x pie && sudo mv pie /usr/local/bin/
pie --version
```

Standalone (unarchived) binaries are also attached to each release as
`pie_<os>_<arch>`, alongside a `checksums.txt` with SHA-256 sums for every asset.

## Quick start

```sh
pie init          # interactive wizard → scaffolds ~/.pie/config.toml
pie run PROJ-123  # one ticket → one PR
pie               # the interactive TUI hub
```

Other commands: `pie start` / `pie stop` (background daemon that polls for tickets),
`pie status`, `pie doctor`, `pie logs`.

## What it does today

- **Agent:** Claude Code (`claude` CLI) — the agent implements the change *and*
  verifies it against the project's own build and tests.
- **Platform:** Android — emulator boot/verify is managed for you; builds run
  through the project's own Gradle setup.
- **Tickets:** Jira Cloud or local `.md` ticket files — no Jira required.
- **Safety:** every ticket runs in its own git worktree, and the pipeline always
  stops at an open PR for human review.

Support for additional agentic CLIs (e.g. Codex) and platforms (iOS) is on the
roadmap but not shipped yet.

## Requirements

- macOS or Linux
- `git`, `gh` (authenticated), and the `claude` CLI on your PATH
- Android SDK with an AVD, for the emulator verify stage
